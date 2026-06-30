package protocol

import (
	"bytes"
	"os"
	"testing"
)

func TestDaemonEventFixture_matches_testdata(t *testing.T) {
	var got bytes.Buffer

	err := WriteDaemonEventFixture(&got)

	if err != nil {
		t.Fatalf("write daemon event fixture: %v", err)
	}
	want, err := os.ReadFile("testdata/daemon-event-stream.json")
	if err != nil {
		t.Fatalf("read daemon event fixture testdata: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got.Bytes())) {
		t.Fatalf("generated fixture differs from testdata\nwant: %s\ngot:  %s", string(want), got.String())
	}
}

func TestDaemonEventFixture_covers_single_and_multi_task_streams(t *testing.T) {
	fixture := DaemonEventFixture()

	if fixture.Version != EventContractVersion {
		t.Fatalf("unexpected fixture version %q", fixture.Version)
	}
	if len(fixture.SingleTaskStream) == 0 {
		t.Fatal("expected single-task stream frames")
	}
	if len(fixture.MultiTaskStream) == 0 {
		t.Fatal("expected multi-task stream frames")
	}
	if fixture.SingleTaskStream[0].Data.Message != "created" {
		t.Fatalf("unexpected single task payload %#v", fixture.SingleTaskStream[0].Data)
	}
	if fixture.MultiTaskStream[0].Data.TaskID != "task-002" {
		t.Fatalf("unexpected multi task id %q", fixture.MultiTaskStream[0].Data.TaskID)
	}
}
