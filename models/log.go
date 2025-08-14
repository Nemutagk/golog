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

type Driver interface {
	Insert(ctx context.Context, document bson.M) error
}

type mongoDriver struct {
	adapter *mongodb.MongoDBAdapter
}

func (m *mongoDriver) Insert(ctx context.Context, document bson.M) error {
	_, err := m.adapter.Insert(ctx, document)
	return err
}

type Service struct {
	drivers []Driver
}

func NewMongoDriver(conn godb.ConnectionManager) Driver {
	dbName := goenvars.GetEnv("DB_LOGS_CONNECTION", "logs")
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
		log.Fatal("APP_NAME environment variable is not set")
	}

	return &mongoDriver{
		adapter: mongodb.NewMongoDBAdapter(dbConn.Collection(collName)),
	}
}

func NewService(drivers ...Driver) *Service {
	return &Service{drivers: drivers}
}

func (s *Service) CreateLog(ctx context.Context, logData Logger) error {
	jsonBytes, err := json.Marshal(logData)
	if err != nil {
		log.Println("Error marshalling log:", err)
		return err
	}

	var bsonLog bson.M
	if err := json.Unmarshal(jsonBytes, &bsonLog); err != nil {
		log.Println("Error unmarshalling log to bson:", err)
		return err
	}

	bsonLog["_id"] = helper.GetUuidV7()
	bsonLog["created_at"] = time.Now()

	for _, d := range s.drivers {
		if err := d.Insert(ctx, bsonLog); err != nil {
			log.Printf("Error writing log with driver %T: %v", d, err)
		}
	}
	return nil
}
