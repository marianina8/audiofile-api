package oauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type stateVal struct {
	A string `json:"a"`
	N int    `json:"n"`
}

// runStateContract checks the behaviour every State implementation must have.
// newState returns a fresh, empty State whose clock is now.
func runStateContract(t *testing.T, newState func(t *testing.T, now func() time.Time) State) {
	ctx := context.Background()
	fresh := func(t *testing.T) (State, *fakeClock) {
		clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
		return newState(t, clock.Now), clock
	}

	t.Run("missing is ErrNotFound", func(t *testing.T) {
		st, _ := fresh(t)
		var v stateVal
		if err := st.Get(ctx, "code", "x", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get err = %v", err)
		}
		if err := st.Take(ctx, "code", "x", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("Take err = %v", err)
		}
		if err := st.Delete(ctx, "code", "x"); err != nil {
			t.Errorf("Delete of missing = %v, want nil", err)
		}
		if err := st.Get(ctx, "code", "", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get empty id err = %v", err)
		}
	})

	t.Run("put get overwrite delete", func(t *testing.T) {
		st, _ := fresh(t)
		if err := st.Put(ctx, "client", "a", stateVal{A: "one", N: 1}, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := st.Put(ctx, "client", "a", stateVal{A: "two", N: 2}, time.Hour); err != nil {
			t.Fatal(err)
		}
		var v stateVal
		if err := st.Get(ctx, "client", "a", &v); err != nil || v != (stateVal{A: "two", N: 2}) {
			t.Fatalf("Get = %+v, %v", v, err)
		}
		if err := st.Delete(ctx, "client", "a"); err != nil {
			t.Fatal(err)
		}
		if err := st.Get(ctx, "client", "a", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get after Delete err = %v", err)
		}
	})

	t.Run("kinds are separate", func(t *testing.T) {
		st, _ := fresh(t)
		if err := st.Put(ctx, "client", "a", stateVal{A: "c"}, time.Hour); err != nil {
			t.Fatal(err)
		}
		var v stateVal
		if err := st.Get(ctx, "code", "a", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("other kind err = %v", err)
		}
	})

	t.Run("expired items are not found", func(t *testing.T) {
		st, clock := fresh(t)
		if err := st.Put(ctx, "pending", "p", stateVal{A: "p"}, 10*time.Minute); err != nil {
			t.Fatal(err)
		}
		clock.advance(9 * time.Minute)
		var v stateVal
		if err := st.Get(ctx, "pending", "p", &v); err != nil {
			t.Fatalf("Get before expiry: %v", err)
		}
		clock.advance(time.Minute)
		if err := st.Get(ctx, "pending", "p", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get at expiry err = %v", err)
		}
		if err := st.Take(ctx, "pending", "p", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("Take after expiry err = %v", err)
		}
	})

	t.Run("take is single use", func(t *testing.T) {
		st, _ := fresh(t)
		if err := st.Put(ctx, "code", "c", stateVal{A: "tok", N: 7}, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
		var v stateVal
		if err := st.Take(ctx, "code", "c", &v); err != nil || v != (stateVal{A: "tok", N: 7}) {
			t.Fatalf("Take = %+v, %v", v, err)
		}
		if err := st.Take(ctx, "code", "c", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("second Take err = %v", err)
		}
		if err := st.Get(ctx, "code", "c", &v); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get after Take err = %v", err)
		}
	})

	t.Run("concurrent take succeeds once", func(t *testing.T) {
		st, _ := fresh(t)
		if err := st.Put(ctx, "code", "race", stateVal{A: "x"}, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var v stateVal
				err := st.Take(ctx, "code", "race", &v)
				switch {
				case err == nil:
					wins.Add(1)
				case !errors.Is(err, ErrNotFound):
					t.Errorf("Take err = %v", err)
				}
			}()
		}
		wg.Wait()
		if n := wins.Load(); n != 1 {
			t.Errorf("successful Takes = %d, want 1", n)
		}
	})

	t.Run("invalid arguments", func(t *testing.T) {
		st, _ := fresh(t)
		if err := st.Put(ctx, "", "a", stateVal{}, time.Hour); err == nil {
			t.Error("empty kind accepted")
		}
		if err := st.Put(ctx, "a#b", "a", stateVal{}, time.Hour); err == nil {
			t.Error("kind with # accepted")
		}
		if err := st.Put(ctx, "code", "", stateVal{}, time.Hour); err == nil {
			t.Error("empty id accepted")
		}
		if err := st.Put(ctx, "code", "a", stateVal{}, 0); err == nil {
			t.Error("zero ttl accepted")
		}
	})
}

func TestMemoryState(t *testing.T) {
	runStateContract(t, func(t *testing.T, now func() time.Time) State {
		st := NewMemoryState()
		st.Now = now
		return st
	})
}

// TestDynamoState runs the contract against DynamoDB Local. It is skipped
// unless DYNAMODB_ENDPOINT is set (see `make test-dynamo`).
func TestDynamoState(t *testing.T) {
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set; run `make test-dynamo` to test against DynamoDB Local")
	}
	client := dynamodb.New(dynamodb.Options{
		Region:       "us-west-2",
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		BaseEndpoint: aws.String(endpoint),
	})
	n := 0
	runStateContract(t, func(t *testing.T, now func() time.Time) State {
		n++
		table := fmt.Sprintf("t%d-%d-bridge", time.Now().UnixNano(), n)
		ctx := context.Background()
		if _, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName:            aws.String(table),
			AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: types.ScalarAttributeTypeS}},
			KeySchema:            []types.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: types.KeyTypeHash}},
			BillingMode:          types.BillingModePayPerRequest,
		}); err != nil {
			t.Fatal(err)
		}
		if err := dynamodb.NewTableExistsWaiter(client).Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 30*time.Second); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)}); err != nil && !strings.Contains(err.Error(), "ResourceNotFound") {
				t.Logf("delete table %s: %v", table, err)
			}
		})
		st := NewDynamoState(client, table)
		st.Now = now
		return st
	})
}
