package log_test

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/birdordev/plumego/log"
)

func ExampleNewLogger() {
	var out bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &out,
	})

	logger.Info("server started", log.Fields{"addr": ":8080"})

	var entry map[string]any
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		panic(err)
	}

	fmt.Println(entry["level"])
	fmt.Println(entry["msg"])
	fmt.Println(entry["addr"])

	// Output:
	// INFO
	// server started
	// :8080
}
