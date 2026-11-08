package streaming_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/ai/sse"
	"github.com/birdordev/plumego/x/ai/streaming"
)

func ExampleStreamManager() {
	ctx := context.Background()
	recorder := httptest.NewRecorder()
	stream, _ := sse.NewStream(ctx, recorder)

	manager := streaming.NewStreamManager()
	if err := manager.Register("workflow-1", stream); err != nil {
		panic(err)
	}

	_ = manager.SendUpdate("workflow-1", &streaming.ProgressUpdate{
		WorkflowID: "workflow-1",
		StepName:   "fetch-context",
		Status:     streaming.StatusStarted,
		Progress:   0.25,
		Timestamp:  time.Unix(0, 0).UTC(),
	})

	body := recorder.Body.String()

	fmt.Println(manager.Count())
	fmt.Println(strings.Contains(body, "event: progress"))
	fmt.Println(strings.Contains(body, "\"workflow_id\":\"workflow-1\""))

	// Output:
	// 1
	// true
	// true
}
