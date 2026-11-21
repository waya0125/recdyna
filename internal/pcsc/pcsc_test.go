// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package pcsc

import (
	"bytes"
	"io"
	"net"
	"path/filepath"
	"testing"
)

// fakePCSCD は pcscd の作り物。版 4.4 だけを受け付け、リーダーを 2 つ見せる（カードは 2 つ目にだけある）。
func fakePCSCD(t *testing.T, conn net.Conn) {
	defer conn.Close()
	read := func(n int) []byte {
		b := make([]byte, n)
		if _, err := io.ReadFull(conn, b); err != nil {
			return nil
		}
		return b
	}
	for {
		h := read(8)
		if h == nil {
			return
		}
		size, command := order.Uint32(h), order.Uint32(h[4:])
		body := read(int(size))
		switch command {
		case cmdVersion:
			if order.Uint32(body) != 4 || order.Uint32(body[4:]) != 4 {
				order.PutUint32(body[8:], 0x8010001e)
			}
			conn.Write(body)
		case cmdEstablishContext:
			order.PutUint32(body[4:], 0x1234)
			conn.Write(body)
		case cmdGetReadersState:
			states := make([]byte, maxReaders*readerStateSize)
			copy(states, "Empty Reader 00 00")
			copy(states[readerStateSize:], "ACS ACR39U 01 00")
			order.PutUint32(states[readerStateSize+maxReaderName+4:], statePresent|0x20)
			conn.Write(states)
		case cmdConnect:
			name := string(bytes.TrimRight(body[4:4+maxReaderName], "\x00"))
			if name != "ACS ACR39U 01 00" || order.Uint32(body) != 0x1234 {
				order.PutUint32(body[len(body)-4:], 0x80100009)
			}
			order.PutUint32(body[4+maxReaderName+8:], 77)
			conn.Write(body)
		case cmdTransmit:
			apdu := read(int(order.Uint32(body[12:])))
			response := append(append([]byte{}, apdu...), 0x90, 0x00)
			order.PutUint32(body[24:], uint32(len(response)))
			conn.Write(append(body, response...))
		case cmdDisconnect, cmdReleaseContext:
			conn.Write(body)
		default:
			t.Errorf("知らない命令 %#x", command)
			return
		}
	}
}

func TestTalksToPCSCD(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "pcscd.comm")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go fakePCSCD(t, conn)
		}
	}()
	t.Setenv("PCSCLITE_CSOCK_NAME", socket)
	r, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Name != "ACS ACR39U 01 00" {
		t.Fatalf("リーダー %q", r.Name)
	}
	for range 2 { // つなぎ直しても使える
		if err := r.Reset(); err != nil {
			t.Fatal(err)
		}
		got, err := r.Transmit([]byte{0x90, 0x30, 0x00, 0x00, 0x00})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte{0x90, 0x30, 0x00, 0x00, 0x00, 0x90, 0x00}) {
			t.Fatalf("% x", got)
		}
	}
	if readerStateSize != 184 {
		t.Fatalf("リーダーの状態の大きさ %d", readerStateSize)
	}
}
