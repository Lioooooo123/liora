package tuisession

import (
	"encoding/json"

	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func eventPayload(event taskpkg.Event) (taskpkg.EventPayload, error) {
	var payload taskpkg.EventPayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return taskpkg.EventPayload{}, err
	}
	return payload, nil
}
