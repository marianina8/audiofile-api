// Command api is the audiofile-api Lambda: one HTTP handler for every route.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/google/uuid"

	"github.com/marianina8/audiofile-api/internal/api"
	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/store"
)

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	deps := api.Deps{
		Store: store.NewDynamo(dynamodb.NewFromConfig(cfg), store.Tables{
			Tracks:    os.Getenv("TRACKS_TABLE"),
			Artists:   os.Getenv("ARTISTS_TABLE"),
			Albums:    os.Getenv("ALBUMS_TABLE"),
			Playlists: os.Getenv("PLAYLISTS_TABLE"),
		}),
		Blob:  blob.NewS3(s3.NewFromConfig(cfg), os.Getenv("BUCKET")),
		Now:   time.Now,
		NewID: uuid.NewString,
	}
	lambda.Start(httpadapter.NewV2(api.New(deps)).ProxyWithContext)
}
