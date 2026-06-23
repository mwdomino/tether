package main

import "testing"

func TestChildCommandRequiresDash(t *testing.T) {
	_, _, err := childCommand([]string{"aws", "sso", "login"}, -1)
	if err == nil {
		t.Fatalf("expected error when -- is missing")
	}
}

func TestChildCommandErrorsWhenEmptyAfterDash(t *testing.T) {
	_, _, err := childCommand(nil, 0)
	if err == nil {
		t.Fatalf("expected error when no command follows --")
	}
}

func TestChildCommandSplitsAfterDash(t *testing.T) {
	name, args, err := childCommand([]string{"aws", "sso", "login"}, 0)
	if err != nil {
		t.Fatalf("childCommand: %v", err)
	}
	if name != "aws" {
		t.Fatalf("name = %q, want aws", name)
	}
	if len(args) != 2 || args[0] != "sso" || args[1] != "login" {
		t.Fatalf("args = %v, want [sso login]", args)
	}
}

func TestChildCommandIgnoresArgsBeforeDash(t *testing.T) {
	name, args, err := childCommand([]string{"stray", "gh", "auth", "login"}, 1)
	if err != nil {
		t.Fatalf("childCommand: %v", err)
	}
	if name != "gh" {
		t.Fatalf("name = %q, want gh", name)
	}
	if len(args) != 2 || args[0] != "auth" || args[1] != "login" {
		t.Fatalf("args = %v, want [auth login]", args)
	}
}
