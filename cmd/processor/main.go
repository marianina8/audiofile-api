// Command processor is the S3-triggered Lambda that analyses uploaded tracks.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/marianina8/audiofile-api/internal/audio"
	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/processor"
	"github.com/marianina8/audiofile-api/internal/store"
)

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	p := &processor.Processor{
		Store: store.NewDynamo(dynamodb.NewFromConfig(cfg), store.Tables{
			Tracks:  os.Getenv("TRACKS_TABLE"),
			Artists: os.Getenv("ARTISTS_TABLE"),
			Albums:  os.Getenv("ALBUMS_TABLE"),
		}),
		Blob:     blob.NewS3(s3.NewFromConfig(cfg), os.Getenv("BUCKET")),
		Analyzer: audio.ToolsFromEnv(),
		Now:      time.Now,
		TmpDir:   os.TempDir(),
	}
	lambda.Start(func(ctx context.Context, ev events.S3Event) error {
		return handle(ctx, ev, p.Process)
	})
}
