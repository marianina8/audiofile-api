package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/marianina8/audiofile-api/internal/model"
)

// Tables holds the DynamoDB table names.
type Tables struct {
	Tracks, Artists, Albums, Playlists string
}

// Index names, matching template.yaml.
const (
	indexByArtist = "by-artist" // tracks (title) and albums (year)
	indexByAlbum  = "by-album"  // tracks (track_number); sparse
)

// Dynamo is a Store backed by four on-demand DynamoDB tables.
type Dynamo struct {
	db *dynamodb.Client
	t  Tables
}

var _ Store = (*Dynamo)(nil)

// NewDynamo returns a Store over the given tables.
func NewDynamo(client *dynamodb.Client, t Tables) *Dynamo {
	return &Dynamo{db: client, t: t}
}

// marshal encodes v and drops empty-string attributes. DynamoDB rejects empty
// strings in key attributes (including GSI keys), and an absent attribute
// unmarshals to the zero value anyway. For album_id it also keeps by-album sparse.
func marshal(v any) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(v)
	if err != nil {
		return nil, err
	}
	for k, av := range item {
		if s, ok := av.(*types.AttributeValueMemberS); ok && s.Value == "" {
			delete(item, k)
		}
	}
	return item, nil
}

func idKey(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}}
}

func isConditionFailed(err error) bool {
	var cf *types.ConditionalCheckFailedException
	return errors.As(err, &cf)
}

func (d *Dynamo) put(ctx context.Context, table string, v any) error {
	item, err := marshal(v)
	if err != nil {
		return err
	}
	_, err = d.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: item})
	return err
}

// ensure creates the item if absent and never overwrites.
func (d *Dynamo) ensure(ctx context.Context, table string, v any) error {
	item, err := marshal(v)
	if err != nil {
		return err
	}
	if _, ok := item["id"]; !ok {
		return errors.New("empty id")
	}
	_, err = d.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(table),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(id)"),
	})
	if err != nil && !isConditionFailed(err) {
		return err
	}
	return nil
}

func (d *Dynamo) get(ctx context.Context, table, entity, id string, out any) error {
	if id == "" { // empty strings cannot be keys; nothing can exist under one
		return fmt.Errorf("%s %q: %w", entity, id, ErrNotFound)
	}
	res, err := d.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(table),
		Key:            idKey(id),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	if res.Item == nil {
		return fmt.Errorf("%s %q: %w", entity, id, ErrNotFound)
	}
	return attributevalue.UnmarshalMap(res.Item, out)
}

func (d *Dynamo) del(ctx context.Context, table, entity, id string) error {
	if id == "" {
		return fmt.Errorf("%s %q: %w", entity, id, ErrNotFound)
	}
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:           aws.String(table),
		Key:                 idKey(id),
		ConditionExpression: aws.String("attribute_exists(id)"),
	})
	if isConditionFailed(err) {
		return fmt.Errorf("%s %q: %w", entity, id, ErrNotFound)
	}
	return err
}

// scanAll reads every page of a table into []T, sorted by id.
func scanAll[T any](ctx context.Context, d *Dynamo, table string, id func(T) string) ([]T, error) {
	out := []T{}
	p := dynamodb.NewScanPaginator(d.db, &dynamodb.ScanInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		var items []T
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &items); err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	slices.SortFunc(out, func(a, b T) int { return cmpString(id(a), id(b)) })
	return out, nil
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// queryIndex reads every item with the given partition key from a GSI, in sort-key order.
func queryIndex[T any](ctx context.Context, d *Dynamo, table, index, pkAttr, pk string) ([]T, error) {
	out := []T{}
	if pk == "" { // empty strings cannot be keys; no item is indexed under one
		return out, nil
	}
	p := dynamodb.NewQueryPaginator(d.db, &dynamodb.QueryInput{
		TableName:                 aws.String(table),
		IndexName:                 aws.String(index),
		KeyConditionExpression:    aws.String("#pk = :pk"),
		ExpressionAttributeNames:  map[string]string{"#pk": pkAttr},
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		var items []T
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &items); err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// --- tracks ---

func (d *Dynamo) PutTrack(ctx context.Context, t model.Track) error {
	return d.put(ctx, d.t.Tracks, t)
}

func (d *Dynamo) GetTrack(ctx context.Context, id string) (model.Track, error) {
	var t model.Track
	err := d.get(ctx, d.t.Tracks, "track", id, &t)
	return t, err
}

func (d *Dynamo) DeleteTrack(ctx context.Context, id string) error {
	return d.del(ctx, d.t.Tracks, "track", id)
}

type pageCursor struct {
	ID string `json:"id"`
}

func decodeCursor(c string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", fmt.Errorf("invalid cursor: %w", err)
	}
	var pc pageCursor
	if err := json.Unmarshal(b, &pc); err != nil || pc.ID == "" {
		return "", fmt.Errorf("invalid cursor")
	}
	return pc.ID, nil
}

func encodeCursor(id string) string {
	b, _ := json.Marshal(pageCursor{ID: id}) // a struct of one string cannot fail to marshal
	return base64.RawURLEncoding.EncodeToString(b)
}

// ListTracks pages with Scan, so order is DynamoDB's key-hash order, stable
// between pages but not id order. It reads limit+1 items to know whether a
// next page exists, so the cursor is empty exactly when the last item was
// returned. limit<=0 means no limit.
func (d *Dynamo) ListTracks(ctx context.Context, limit int, cursor string) ([]model.Track, string, error) {
	var start map[string]types.AttributeValue
	if cursor != "" {
		id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		start = idKey(id)
	}
	if limit <= 0 {
		if start != nil {
			return nil, "", errors.New("cursor requires a limit")
		}
		all, err := d.AllTracks(ctx)
		return all, "", err
	}
	out := []model.Track{}
	for len(out) <= limit {
		res, err := d.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(d.t.Tracks),
			Limit:             aws.Int32(int32(limit + 1 - len(out))),
			ExclusiveStartKey: start,
			ConsistentRead:    aws.Bool(true),
		})
		if err != nil {
			return nil, "", err
		}
		var items []model.Track
		if err := attributevalue.UnmarshalListOfMaps(res.Items, &items); err != nil {
			return nil, "", err
		}
		out = append(out, items...)
		if res.LastEvaluatedKey == nil {
			break
		}
		start = res.LastEvaluatedKey
	}
	if len(out) <= limit {
		return out, "", nil
	}
	return out[:limit], encodeCursor(out[limit-1].ID), nil
}

func (d *Dynamo) AllTracks(ctx context.Context) ([]model.Track, error) {
	return scanAll(ctx, d, d.t.Tracks, func(t model.Track) string { return t.ID })
}

func (d *Dynamo) TracksByArtist(ctx context.Context, artistID string) ([]model.Track, error) {
	return queryIndex[model.Track](ctx, d, d.t.Tracks, indexByArtist, "artist_id", artistID)
}

func (d *Dynamo) TracksByAlbum(ctx context.Context, albumID string) ([]model.Track, error) {
	return queryIndex[model.Track](ctx, d, d.t.Tracks, indexByAlbum, "album_id", albumID)
}

// --- artists ---

func (d *Dynamo) EnsureArtist(ctx context.Context, a model.Artist) error {
	return d.ensure(ctx, d.t.Artists, a)
}

func (d *Dynamo) GetArtist(ctx context.Context, id string) (model.Artist, error) {
	var a model.Artist
	err := d.get(ctx, d.t.Artists, "artist", id, &a)
	return a, err
}

func (d *Dynamo) ListArtists(ctx context.Context) ([]model.Artist, error) {
	return scanAll(ctx, d, d.t.Artists, func(a model.Artist) string { return a.ID })
}

// --- albums ---

func (d *Dynamo) EnsureAlbum(ctx context.Context, a model.Album) error {
	return d.ensure(ctx, d.t.Albums, a)
}

func (d *Dynamo) GetAlbum(ctx context.Context, id string) (model.Album, error) {
	var a model.Album
	err := d.get(ctx, d.t.Albums, "album", id, &a)
	return a, err
}

func (d *Dynamo) ListAlbums(ctx context.Context) ([]model.Album, error) {
	return scanAll(ctx, d, d.t.Albums, func(a model.Album) string { return a.ID })
}

func (d *Dynamo) AlbumsByArtist(ctx context.Context, artistID string) ([]model.Album, error) {
	return queryIndex[model.Album](ctx, d, d.t.Albums, indexByArtist, "artist_id", artistID)
}

// --- playlists ---

func (d *Dynamo) PutPlaylist(ctx context.Context, p model.Playlist) error {
	return d.put(ctx, d.t.Playlists, p)
}

func (d *Dynamo) GetPlaylist(ctx context.Context, id string) (model.Playlist, error) {
	var p model.Playlist
	err := d.get(ctx, d.t.Playlists, "playlist", id, &p)
	return p, err
}

func (d *Dynamo) ListPlaylists(ctx context.Context) ([]model.Playlist, error) {
	return scanAll(ctx, d, d.t.Playlists, func(p model.Playlist) string { return p.ID })
}

func (d *Dynamo) DeletePlaylist(ctx context.Context, id string) error {
	return d.del(ctx, d.t.Playlists, "playlist", id)
}

// --- test support ---

func attr(name string, typ types.ScalarAttributeType) types.AttributeDefinition {
	return types.AttributeDefinition{AttributeName: aws.String(name), AttributeType: typ}
}

func keys(pk string, sk string) []types.KeySchemaElement {
	ks := []types.KeySchemaElement{{AttributeName: aws.String(pk), KeyType: types.KeyTypeHash}}
	if sk != "" {
		ks = append(ks, types.KeySchemaElement{AttributeName: aws.String(sk), KeyType: types.KeyTypeRange})
	}
	return ks
}

func gsi(name, pk, sk string) types.GlobalSecondaryIndex {
	return types.GlobalSecondaryIndex{
		IndexName:  aws.String(name),
		KeySchema:  keys(pk, sk),
		Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll},
	}
}

// CreateTablesForTest creates the four tables with the same keys and GSIs as
// template.yaml. It is used only by tests.
func CreateTablesForTest(ctx context.Context, client *dynamodb.Client, t Tables) error {
	id := attr("id", types.ScalarAttributeTypeS)
	specs := []dynamodb.CreateTableInput{
		{
			TableName: aws.String(t.Tracks),
			AttributeDefinitions: []types.AttributeDefinition{
				id,
				attr("artist_id", types.ScalarAttributeTypeS), attr("title", types.ScalarAttributeTypeS),
				attr("album_id", types.ScalarAttributeTypeS), attr("track_number", types.ScalarAttributeTypeN),
			},
			GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
				gsi(indexByArtist, "artist_id", "title"),
				gsi(indexByAlbum, "album_id", "track_number"),
			},
		},
		{TableName: aws.String(t.Artists), AttributeDefinitions: []types.AttributeDefinition{id}},
		{
			TableName: aws.String(t.Albums),
			AttributeDefinitions: []types.AttributeDefinition{
				id, attr("artist_id", types.ScalarAttributeTypeS), attr("year", types.ScalarAttributeTypeN),
			},
			GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{gsi(indexByArtist, "artist_id", "year")},
		},
		{TableName: aws.String(t.Playlists), AttributeDefinitions: []types.AttributeDefinition{id}},
	}
	waiter := dynamodb.NewTableExistsWaiter(client)
	for _, in := range specs {
		in.KeySchema = keys("id", "")
		in.BillingMode = types.BillingModePayPerRequest
		if _, err := client.CreateTable(ctx, &in); err != nil {
			return fmt.Errorf("create table %s: %w", *in.TableName, err)
		}
		if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: in.TableName}, 30*time.Second); err != nil {
			return fmt.Errorf("wait for table %s: %w", *in.TableName, err)
		}
	}
	return nil
}
