package helper

import (
	"encoding/json"
	"fmt"

	"github.com/gofrs/uuid"
)

func GetUuidV7() string {
	return uuid.Must(uuid.NewV7()).String()
}

func PrettyPrint(data any, print *bool) string {
	// Convertir el mapa a JSON con sangría
	prettyJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Println("Error formatting JSON:", err)
		return ""
	}

	if print == nil {
		print = new(bool)
	}

	if *print {
		fmt.Println(string(prettyJSON))
	}

	return string(prettyJSON)
}
