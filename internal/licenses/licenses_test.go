package licenses

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetect(t *testing.T) {
	for text, want := range map[string]string{
		"Permission is hereby granted, free of charge, to any person":               "MIT",
		"Apache License\nVersion 2.0, January 2004":                                 "Apache-2.0",
		"Redistribution and use in source and binary forms ... Neither the name of": "BSD-3-Clause",
		"Redistribution and use in source and binary forms, with or without":        "BSD-2-Clause",
		"This is free and unencumbered software released into the public domain.":   "Unlicense",
		"": "",
	} {
		if got := Detect(text); got != want {
			t.Errorf("Detect(%.30q) = %q, want %q", text, got, want)
		}
	}
}

// TestCollect lists this module's own dependencies.
func TestCollect(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	ls, err := Collect(root, "./cmd/giode", "")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]License{}
	for _, l := range ls {
		found[l.Module] = l
	}
	if l := found["Go standard library"]; l.Type != "BSD-3-Clause" {
		t.Errorf("Go's license: %q", l.Type)
	}
	if l := found["github.com/BurntSushi/toml"]; l.Type != "MIT" || l.Text == "" {
		t.Errorf("toml's license: %+v", l.Type)
	}
	if _, ok := found["github.com/xmhrnx/giode-cli"]; ok {
		t.Error("the main module is listed")
	}
}
