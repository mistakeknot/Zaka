package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestAgentArgsAreRepeatableAndOrdered(t *testing.T) {
	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	var args stringListFlag
	fs.Var(&args, "agent-arg", "")
	if err := fs.Parse([]string{
		"--agent-arg", "-c",
		"--agent-arg", "model_reasoning_effort=high",
		"--agent-arg", "-c",
		"--agent-arg", "service_tier=default",
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"-c", "model_reasoning_effort=high", "-c", "service_tier=default"}
	if !reflect.DeepEqual([]string(args), want) {
		t.Fatalf("agent args = %q, want %q", args, want)
	}
}
