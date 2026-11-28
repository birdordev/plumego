package migrations

import (
	"embed"
	"fmt"
)

//go:embed sql/[0-9]*.sql sql/queue.sql
var files embed.FS

func ReadFile(name string) (string, error) {
	data, err := files.ReadFile("sql/" + name)
	if err != nil {
		return "", fmt.Errorf("read migration %s: %w", name, err)
	}
	return string(data), nil
}
