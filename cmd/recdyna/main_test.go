// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package main

import (
	"bytes"
	"testing"
)

func TestParseArgs(t *testing.T) {
	var stderr bytes.Buffer
	o, err := parseArgs([]string{"--b25", "--strip", "--stats", "21", "10", "/tmp/out.ts"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !o.b25 || !o.strip || !o.stats || o.channel != 21 || o.freqKHz != 521143 || o.seconds != 10 || o.output != "/tmp/out.ts" {
		t.Fatalf("unexpected options: %+v", o)
	}

	// CATV + 無制限 + 標準出力
	o2, err := parseArgs([]string{"C13", "-", "-"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs CATV failed: %v", err)
	}
	if !o2.catv || o2.channel != 13 || o2.freqKHz != 111143 || o2.seconds != 0 || o2.output != "-" {
		t.Fatalf("unexpected CATV options: %+v", o2)
	}

	// --check モード
	o3, err := parseArgs([]string{"--check", "27"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs --check failed: %v", err)
	}
	if !o3.check || o3.channel != 27 || o3.freqKHz != 557143 {
		t.Fatalf("unexpected --check options: %+v", o3)
	}

	// --pcsc 単体はエラー
	if _, err := parseArgs([]string{"--pcsc", "21", "5", "-"}, &stderr); err == nil {
		t.Fatalf("expected error for --pcsc without --b25")
	}
}
