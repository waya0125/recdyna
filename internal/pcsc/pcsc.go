// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package pcsc は pcscd（pcsc-lite）の UNIX ソケットと直接話して、カードリーダーの T=1 のカードを使う。
// libpcsclite も cgo も使わない。
//
// 通信の形（メッセージの見出しと構造体）は pcsc-lite の src/winscard_msg.h・src/eventhandler.h に合わせた。
// 通信の版は 4.4 を名乗る（pcscd 1.9 系はこれだけを受け付け、2.x 系も 4.4 を受け付ける）。
package pcsc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// DefaultSocket は pcscd の窓口。環境変数 PCSCLITE_CSOCK_NAME で変えられる（libpcsclite と同じ）。
const DefaultSocket = "/run/pcscd/pcscd.comm"

const (
	protocolMajor = 4
	protocolMinor = 4

	maxReaderName   = 128
	maxReaders      = 16
	maxATRSize      = 33
	readerStateSize = maxReaderName + 4 + 4 + 4 + maxATRSize + 3 + 4 + 4 // 184

	cmdEstablishContext = 0x01
	cmdReleaseContext   = 0x02
	cmdConnect          = 0x04
	cmdDisconnect       = 0x06
	cmdTransmit         = 0x09
	cmdVersion          = 0x11
	cmdGetReadersState  = 0x12

	scopeUser    = 0x0000
	shareShared  = 0x0002
	protocolT1   = 0x0002
	leaveCard    = 0x0000
	resetCard    = 0x0001
	pciLength    = 8
	recvMax      = 4096
	statePresent = 0x0004 // SCARD_PRESENT（readerState のビット）
)

var order = binary.NativeEndian

// Error は pcscd の返した失敗の番号。
type Error uint32

func (e Error) Error() string { return fmt.Sprintf("pcscd の答え %#08x", uint32(e)) }

// Reader は 1 つのカードリーダーにつないだもの。bcas.Transport として使える。
type Reader struct {
	conn    net.Conn
	context uint32
	card    int32
	name    string
	// Name は使っているリーダーの名前
	Name string
}

// Socket は使う pcscd の窓口。
func Socket() string {
	if s := os.Getenv("PCSCLITE_CSOCK_NAME"); s != "" {
		return s
	}
	return DefaultSocket
}

// Open は pcscd につなぎ、カードの入っている最初のリーダーを選ぶ（まだカードにはつながない）。
func Open() (*Reader, error) {
	conn, err := net.DialTimeout("unix", Socket(), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("pcscd（%s）につながらない: %w", Socket(), err)
	}
	r := &Reader{conn: conn}
	if err := r.handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	names, err := r.readers()
	if err != nil {
		r.Close()
		return nil, err
	}
	if len(names) == 0 {
		r.Close()
		return nil, errors.New("カードの入ったリーダーが無い")
	}
	r.name = names[0]
	r.Name = names[0]
	return r, nil
}

func (r *Reader) send(command uint32, body []byte) error {
	header := make([]byte, 8, 8+len(body))
	order.PutUint32(header, uint32(len(body)))
	order.PutUint32(header[4:], command)
	r.conn.SetDeadline(time.Now().Add(30 * time.Second))
	_, err := r.conn.Write(append(header, body...))
	return err
}

func (r *Reader) receive(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r.conn, buf)
	return buf, err
}

func u32s(values ...uint32) []byte {
	b := make([]byte, 4*len(values))
	for i, v := range values {
		order.PutUint32(b[4*i:], v)
	}
	return b
}

// call は構造体 body を送り、同じ大きさの答えを受け取って、最後の rv を確かめる。
func (r *Reader) call(command uint32, body []byte) ([]byte, error) {
	if err := r.send(command, body); err != nil {
		return nil, err
	}
	answer, err := r.receive(len(body))
	if err != nil {
		return nil, err
	}
	if rv := order.Uint32(answer[len(answer)-4:]); rv != 0 {
		return answer, Error(rv)
	}
	return answer, nil
}

func (r *Reader) handshake() error {
	answer, err := r.call(cmdVersion, u32s(protocolMajor, protocolMinor, 0))
	if err != nil {
		if answer != nil {
			return fmt.Errorf("pcscd の通信の版が合わない（pcscd は %d.%d、こちらは %d.%d）",
				int32(order.Uint32(answer)), int32(order.Uint32(answer[4:])), protocolMajor, protocolMinor)
		}
		return err
	}
	answer, err = r.call(cmdEstablishContext, u32s(scopeUser, 0, 0))
	if err != nil {
		return fmt.Errorf("pcscd の文脈を作れない: %w", err)
	}
	r.context = order.Uint32(answer[4:])
	return nil
}

// readers はカードの入っているリーダーの名前を返す。
func (r *Reader) readers() ([]string, error) {
	if err := r.send(cmdGetReadersState, nil); err != nil {
		return nil, err
	}
	states, err := r.receive(maxReaders * readerStateSize)
	if err != nil {
		return nil, err
	}
	var names []string
	for i := range maxReaders {
		s := states[i*readerStateSize : (i+1)*readerStateSize]
		name := s[:maxReaderName]
		if j := bytes.IndexByte(name, 0); j >= 0 {
			name = name[:j]
		}
		state := order.Uint32(s[maxReaderName+4:])
		if len(name) > 0 && state&statePresent != 0 {
			names = append(names, string(name))
		}
	}
	return names, nil
}

// Reset はカードにつなぎ直す（つないでいれば、カードをリセットして切ってから）。
func (r *Reader) Reset() error {
	if r.card != 0 {
		_, _ = r.call(cmdDisconnect, u32s(uint32(r.card), resetCard, 0))
		r.card = 0
	}
	body := make([]byte, 4+maxReaderName+5*4)
	order.PutUint32(body, r.context)
	copy(body[4:4+maxReaderName-1], r.name)
	order.PutUint32(body[4+maxReaderName:], shareShared)
	order.PutUint32(body[4+maxReaderName+4:], protocolT1)
	answer, err := r.call(cmdConnect, body)
	if err != nil {
		return fmt.Errorf("リーダー %q のカードにつながらない: %w", r.name, err)
	}
	r.card = int32(order.Uint32(answer[4+maxReaderName+8:]))
	return nil
}

// Transmit は APDU を T=1 で送り、答えを受け取る。
func (r *Reader) Transmit(apdu []byte) ([]byte, error) {
	if r.card == 0 {
		return nil, errors.New("カードにつないでいない")
	}
	body := u32s(uint32(r.card), protocolT1, pciLength, uint32(len(apdu)), protocolT1, pciLength, recvMax, 0)
	header := make([]byte, 8)
	order.PutUint32(header, uint32(len(body)))
	order.PutUint32(header[4:], cmdTransmit)
	r.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := r.conn.Write(append(append(header, body...), apdu...)); err != nil {
		return nil, err
	}
	answer, err := r.receive(len(body))
	if err != nil {
		return nil, err
	}
	if rv := order.Uint32(answer[28:]); rv != 0 {
		return nil, Error(rv)
	}
	return r.receive(int(order.Uint32(answer[24:])))
}

// Close はカードとの接続と文脈を返して切る。
func (r *Reader) Close() error {
	if r.card != 0 {
		_, _ = r.call(cmdDisconnect, u32s(uint32(r.card), leaveCard, 0))
		r.card = 0
	}
	if r.context != 0 {
		_, _ = r.call(cmdReleaseContext, u32s(r.context, 0))
		r.context = 0
	}
	return r.conn.Close()
}
