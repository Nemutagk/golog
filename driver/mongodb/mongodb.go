package mongodb

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDBAdapter struct {
	collection *mongo.Collection
}

func NewMongoDBAdapter(collection *mongo.Collection) *MongoDBAdapter {
	return &MongoDBAdapter{
		collection: collection,
	}
}

func (s *MongoDBAdapter) Get(ctx context.Context, filter bson.M, options *options.FindOptions) (*mongo.Cursor, error) {
	return s.collection.Find(ctx, filter, options)
}

func (s *MongoDBAdapter) GetOne(ctx context.Context, filter bson.M, options *options.FindOneOptions) *mongo.SingleResult {
	return s.collection.FindOne(ctx, filter, options)
}

func (s *MongoDBAdapter) Insert(ctx context.Context, document bson.M) (*mongo.InsertOneResult, error) {
	return s.collection.InsertOne(ctx, document)
}

func (s *MongoDBAdapter) Update(ctx context.Context, filter *bson.M, update bson.M) (*mongo.UpdateResult, error) {
	return s.collection.UpdateOne(ctx, filter, update)
}

func (s *MongoDBAdapter) Delete(ctx context.Context, filter *bson.M) (*mongo.DeleteResult, error) {
	return s.collection.DeleteOne(ctx, filter)
}

func (s *MongoDBAdapter) Count(ctx context.Context, filter *bson.M) (int64, error) {
	return s.collection.CountDocuments(ctx, filter)
}
