package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ErrNotFound means the item is absent or past its TTL.
var ErrNotFound = errors.New("not found")

// State is the bridge's short-lived storage: DCR clients, pending authorize
// requests and authorization codes. Items are addressed by (kind, id) and
// expire after their TTL. Values are stored as JSON.
type State interface {
	// Put stores v under (kind, id), replacing any existing item.
	Put(ctx context.Context, kind, id string, v any, ttl time.Duration) error
	// Get decodes the item into v, or returns ErrNotFound.
	Get(ctx context.Context, kind, id string, v any) error
	// Delete removes the item. Deleting a missing item is not an error.
	Delete(ctx context.Context, kind, id string) error
	// Take gets and deletes the item atomically: of several concurrent Takes
	// of one item, exactly one succeeds. Others get ErrNotFound.
	Take(ctx context.Context, kind, id string, v any) error
}

// Item kinds.
const kindClient = "client"

func stateKey(kind, id string) (string, error) {
	if kind == "" || strings.Contains(kind, "#") {
		return "", fmt.Errorf("oauth state: invalid kind %q", kind)
	}
	if id == "" {
		return "", errors.New("oauth state: empty id")
	}
	return kind + "#" + id, nil
}

// expiry returns the expiry in epoch seconds, rounded up so an item never
// expires early.
func expiry(now time.Time, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, fmt.Errorf("oauth state: ttl must be positive, got %v", ttl)
	}
	exp := now.Add(ttl)
	sec := exp.Unix()
	if exp.Nanosecond() > 0 {
		sec++
	}
	return sec, nil
}

// expired reports whether an item with expiry exp (epoch seconds) is gone.
func expired(now time.Time, exp int64) bool { return now.Unix() >= exp }

func clock(now func() time.Time) time.Time {
	if now == nil {
		return time.Now()
	}
	return now()
}

// MemoryState is an in-process State for tests and local runs.
type MemoryState struct {
	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	mu    sync.Mutex
	items map[string]memItem
}

type memItem struct {
	data []byte
	exp  int64
}

var _ State = (*MemoryState)(nil)

// NewMemoryState returns an empty MemoryState.
func NewMemoryState() *MemoryState {
	return &MemoryState{items: map[string]memItem{}}
}

// Put implements State.
func (m *MemoryState) Put(_ context.Context, kind, id string, v any, ttl time.Duration) error {
	key, err := stateKey(kind, id)
	if err != nil {
		return err
	}
	now := clock(m.Now)
	exp, err := expiry(now, ttl)
	if err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, it := range m.items { // drop expired items so the map stays small
		if expired(now, it.exp) {
			delete(m.items, k)
		}
	}
	m.items[key] = memItem{data: data, exp: exp}
	return nil
}

func (m *MemoryState) get(kind, id string, remove bool) ([]byte, error) {
	key, err := stateKey(kind, id)
	if err != nil {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[key]
	if !ok || expired(clock(m.Now), it.exp) {
		return nil, ErrNotFound
	}
	if remove {
		delete(m.items, key)
	}
	return it.data, nil
}

// Get implements State.
func (m *MemoryState) Get(_ context.Context, kind, id string, v any) error {
	data, err := m.get(kind, id, false)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Take implements State.
func (m *MemoryState) Take(_ context.Context, kind, id string, v any) error {
	data, err := m.get(kind, id, true)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Delete implements State.
func (m *MemoryState) Delete(_ context.Context, kind, id string) error {
	key, err := stateKey(kind, id)
	if err != nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}

// DynamoState is a State over one DynamoDB table with string partition key
// `pk` (= kind#id), number attribute `ttl` (epoch seconds; the table's TTL
// attribute) and string attribute `v` (the JSON value). DynamoDB deletes
// expired items lazily, so reads also check `ttl`.
type DynamoState struct {
	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	db    *dynamodb.Client
	table string
}

var _ State = (*DynamoState)(nil)

// NewDynamoState returns a State over table.
func NewDynamoState(client *dynamodb.Client, table string) *DynamoState {
	return &DynamoState{db: client, table: table}
}

const (
	attrPK  = "pk"
	attrTTL = "ttl"
	attrVal = "v"
)

func pkKey(key string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{attrPK: &types.AttributeValueMemberS{Value: key}}
}

// Put implements State.
func (d *DynamoState) Put(ctx context.Context, kind, id string, v any, ttl time.Duration) error {
	key, err := stateKey(kind, id)
	if err != nil {
		return err
	}
	exp, err := expiry(clock(d.Now), ttl)
	if err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = d.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(d.table),
		Item: map[string]types.AttributeValue{
			attrPK:  &types.AttributeValueMemberS{Value: key},
			attrTTL: &types.AttributeValueMemberN{Value: strconv.FormatInt(exp, 10)},
			attrVal: &types.AttributeValueMemberS{Value: string(data)},
		},
	})
	if err != nil {
		return fmt.Errorf("oauth state put %s: %w", kind, err)
	}
	return nil
}

// decodeItem returns ErrNotFound for a missing or expired item and decodes
// the value otherwise.
func (d *DynamoState) decodeItem(item map[string]types.AttributeValue, v any) error {
	if len(item) == 0 {
		return ErrNotFound
	}
	ttlAttr, ok := item[attrTTL].(*types.AttributeValueMemberN)
	if !ok {
		return ErrNotFound
	}
	exp, err := strconv.ParseInt(ttlAttr.Value, 10, 64)
	if err != nil || expired(clock(d.Now), exp) {
		return ErrNotFound
	}
	val, ok := item[attrVal].(*types.AttributeValueMemberS)
	if !ok {
		return errors.New("oauth state: item has no value")
	}
	return json.Unmarshal([]byte(val.Value), v)
}

// Get implements State.
func (d *DynamoState) Get(ctx context.Context, kind, id string, v any) error {
	key, err := stateKey(kind, id)
	if err != nil {
		return ErrNotFound
	}
	out, err := d.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(d.table),
		Key:            pkKey(key),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("oauth state get %s: %w", kind, err)
	}
	return d.decodeItem(out.Item, v)
}

// Take implements State with a conditional DeleteItem, so only one caller
// can delete (and so receive) a live item.
func (d *DynamoState) Take(ctx context.Context, kind, id string, v any) error {
	key, err := stateKey(kind, id)
	if err != nil {
		return ErrNotFound
	}
	now := clock(d.Now).Unix()
	out, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:           aws.String(d.table),
		Key:                 pkKey(key),
		ConditionExpression: aws.String("attribute_exists(#pk) AND #ttl > :now"),
		ExpressionAttributeNames: map[string]string{
			"#pk":  attrPK,
			"#ttl": attrTTL,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":now": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
		},
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		var cf *types.ConditionalCheckFailedException
		if errors.As(err, &cf) {
			return ErrNotFound
		}
		return fmt.Errorf("oauth state take %s: %w", kind, err)
	}
	return d.decodeItem(out.Attributes, v)
}

// Delete implements State.
func (d *DynamoState) Delete(ctx context.Context, kind, id string) error {
	key, err := stateKey(kind, id)
	if err != nil {
		return nil
	}
	if _, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(d.table),
		Key:       pkKey(key),
	}); err != nil {
		return fmt.Errorf("oauth state delete %s: %w", kind, err)
	}
	return nil
}
