// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package multi2

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
)

// testdata/arib25-multi2.txt は、arib25 の multi2.c に作り物の鍵と中身を渡して復号させた結果。
func TestSameAsArib25(t *testing.T) {
	file, err := os.Open("testdata/arib25-multi2.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values := map[string][]byte{}
	var c *Cipher
	scrambling, cases := 0, 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		switch fields[0] {
		case "case":
			scrambling, _ = strconv.Atoi(fields[1])
			continue
		case "sys", "cbc", "key", "in":
			values[fields[0]], _ = hex.DecodeString(fields[1])
			if fields[0] == "key" {
				c = New(values["sys"], values["cbc"], 4)
				c.SetScrambleKey(values["key"])
			}
		case "out":
			want, _ := hex.DecodeString(fields[1])
			got := append([]byte{}, values["in"]...)
			c.Decrypt(scrambling, got)
			if !bytes.Equal(got, want) {
				t.Errorf("鍵 %d・%d バイト: arib25 と違う", scrambling, len(got))
			}
			// 暗号化すると元に戻る
			c.Encrypt(scrambling, got)
			if !bytes.Equal(got, values["in"]) {
				t.Errorf("鍵 %d・%d バイト: 暗号化で戻らない", scrambling, len(got))
			}
			cases++
		}
	}
	if cases != 10 {
		t.Fatalf("%d 件しか比べていない", cases)
	}
}
