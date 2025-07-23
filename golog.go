package golog

import (
	"context"
	"log"
	"runtime"
	"sync"

	"github.com/Nemutagk/godb"
	"github.com/Nemutagk/godb/definitions/db"
	"github.com/Nemutagk/golog/models"
)

var connectManagerOnce sync.Once
var connectionManager godb.ConnectionManager

func Init(listConnections map[string]db.DbConnection) {
	connectManagerOnce.Do(func() {
		connectionManager = *godb.InitConnections(listConnections)
	})
}

func Log(ctx context.Context, args ...interface{}) {
	if connectionManager.Connections == nil {
		panic("Connection manager not initialized. Call Init() first.")
	}

	requestID := ctx.Value("request_id")
	if requestID == nil {
		requestID = "unknown"
	}

	_, file, line, ok := runtime.Caller(1) // 1 para obtener el caller inmediato
	if !ok {
		log.Printf("Log llamado desde %s:%d\n", file, line)
	}

	logEntry := models.NewLog(connectionManager)
	logEntry.CreateLog(ctx, models.Logger{
		Level:     "INFO",
		RequestID: requestID.(string),
		Payload:   args,
		File:      file,
		Line:      line,
	})

	args = append([]interface{}{"[INFO]"}, args...)
	log.Println(args...)
}

func Error(ctx context.Context, args ...interface{}) {
	if connectionManager.Connections == nil {
		panic("Connection manager not initialized. Call Init() first.")
	}

	requestID := ctx.Value("request_id")
	if requestID == nil {
		requestID = "unknown"
	}

	_, file, line, ok := runtime.Caller(1) // 1 para obtener el caller inmediato
	if !ok {
		log.Printf("Log llamado desde %s:%d\n", file, line)
	}

	logEntry := models.NewLog(connectionManager)
	logEntry.CreateLog(ctx, models.Logger{
		Level:     "ERROR",
		RequestID: requestID.(string),
		Payload:   args,
		File:      file,
		Line:      line,
	})

	args = append([]interface{}{"[INFO]"}, args...)
	log.Println(args...)
}

func Debug(ctx context.Context, args ...interface{}) {
	if connectionManager.Connections == nil {
		panic("Connection manager not initialized. Call Init() first.")
	}

	requestID := ctx.Value("request_id")
	if requestID == nil {
		requestID = "unknown"
	}

	_, file, line, ok := runtime.Caller(1) // 1 para obtener el caller inmediato
	if !ok {
		log.Printf("Log llamado desde %s:%d\n", file, line)
	}

	logEntry := models.NewLog(connectionManager)
	logEntry.CreateLog(ctx, models.Logger{
		Level:     "ERROR",
		RequestID: requestID.(string),
		Payload:   args,
		File:      file,
		Line:      line,
	})

	args = append([]interface{}{"[INFO]"}, args...)
	log.Println(args...)
}
