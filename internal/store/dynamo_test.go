package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// TestDynamo runs the shared contract against DynamoDB Local. It is skipped
// unless DYNAMODB_ENDPOINT is set (see `make test-dynamo`).
func TestDynamo(t *testing.T) {
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
	RunContract(t, func(t *testing.T) Store {
		n++
		prefix := fmt.Sprintf("t%d-%d-", time.Now().UnixNano(), n)
		tables := Tables{
			Tracks:    prefix + "tracks",
			Artists:   prefix + "artists",
			Albums:    prefix + "albums",
			Playlists: prefix + "playlists",
		}
		ctx := context.Background()
		if err := CreateTablesForTest(ctx, client, tables); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, name := range []string{tables.Tracks, tables.Artists, tables.Albums, tables.Playlists} {
				if _, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(name)}); err != nil && !strings.Contains(err.Error(), "ResourceNotFound") {
					t.Logf("delete table %s: %v", name, err)
				}
			}
		})
		return NewDynamo(client, tables)
	})
}
