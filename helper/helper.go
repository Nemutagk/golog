package helper

import "github.com/gofrs/uuid"

func GetUuidV7() string {
	return uuid.Must(uuid.NewV7()).String()
}
