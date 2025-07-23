package models

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/Nemutagk/godb"
	"github.com/Nemutagk/goenvars"
	"github.com/Nemutagk/golog/driver/mongodb"
	"github.com/Nemutagk/golog/helper"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type Logger struct {
	Level     string `json:"level" bson:"level"`
	RequestID string `json:"request_id" bson:"request_id"`
	Payload   []any  `json:"payload" bson:"payload"`
	File      string `json:"file" bson:"file"`
	Line      int    `json:"line" bson:"line"`
}

type Service struct {
	repo *mongodb.MongoDBAdapter
}

func NewLog(conn godb.ConnectionManager) *Service {
	dbName := goenvars.GetEnv("DB_LOGS_DATABASE", "logs")
	dbRaw, err := conn.GetRawConnection(dbName)
	if err != nil {
		log.Fatalf("Error getting connection: %v", err)
	}

	dbConn, ok := dbRaw.(*mongo.Database)
	if !ok {
		log.Fatalf("Connection is not a MongoDB database: %T", dbRaw)
	}

	collName := goenvars.GetEnv("APP_NAME", "logs")
	if collName == "" {
		log.Fatal("LOGS_COLLECTION_NAME environment variable is not set")
	}

	return &Service{
		repo: mongodb.NewMongoDBAdapter(dbConn.Collection(collName)),
	}
}

func (s *Service) CreateLog(ctx context.Context, logData Logger) error {
	jsonBytes, err := json.Marshal(logData)
	if err != nil {
		log.Println("Error marshalling log:", err)
		return err
	}

	var bson_log bson.M
	if err := json.Unmarshal(jsonBytes, &bson_log); err != nil {
		log.Println("Error unmarshalling log to bson:", err)
		return err
	}

	bson_log["_id"] = helper.GetUuidV7()
	bson_log["created_at"] = time.Now()

	_, err = s.repo.Insert(ctx, bson_log)
	if err != nil {
		return err
	}

	return nil
}
