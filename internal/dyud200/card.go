// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package dyud200

import (
	"fmt"
	"io"
	"sync"
)

// CardReader は DY-UD200 本体の B-CAS カードスロットを USB 制御コマンド越しに扱う。
// bcas.Transport インターフェースを実装する。
type CardReader struct {
	dev     *Device
	Log     io.Writer
	retries int

	mu    sync.Mutex
	cycle byte // 0x00 と 0x40 を交互に切り替え
}

// NewCardReader は DY-UD200 内蔵 B-CAS スロット用の Transport を作る。
func NewCardReader(dev *Device) *CardReader {
	return &CardReader{
		dev:     dev,
		Log:     io.Discard,
		retries: 3,
	}
}

var (
	cmdResetBCASA = []byte{0x1f, 0xf0, 0xf0, 0x05, 0x02, 0x00, 0x42, 0x00, 0x00}
	cmdResetBCASB = []byte{0x1f, 0xf0, 0xf0, 0x05, 0x02, 0x00, 0x41, 0x00, 0x00}
)

// Reset は本体内蔵の B-CAS コントローラーをリセットする。
func (r *CardReader) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cycle = 0
	if _, err := r.dev.SendCommand(cmdResetBCASA, 128); err != nil {
		return fmt.Errorf("B-CAS リセット A 失敗: %w", err)
	}
	if _, err := r.dev.SendCommand(cmdResetBCASB, 128); err != nil {
		return fmt.Errorf("B-CAS リセット B 失敗: %w", err)
	}
	return nil
}

// Transmit は APDU を内蔵 B-CAS カードへ送り、応答を返す。
func (r *CardReader) Transmit(apdu []byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(apdu)
	if n == 0 || n > 115 {
		return nil, fmt.Errorf("B-CAS APDU 長が不正: %d", n)
	}

	var cmd [128]byte
	cmd[0] = 0x1f
	cmd[1] = 0xf0
	cmd[2] = 0xf0
	cmd[3] = byte(n + 9)
	cmd[4] = 0x02
	cmd[5] = 0x00
	cmd[6] = 0x31
	cmd[7] = 0x00
	cmd[8] = byte(n + 4)
	cmd[9] = 0x00
	cmd[11] = byte(n)
	copy(cmd[12:12+n], apdu)

	var lastErr error
	for attempt := 0; attempt <= r.retries; attempt++ {
		cmd[10] = r.cycle
		r.cycle ^= 0x40

		var parity byte
		for i := 10; i < 12+n; i++ {
			parity ^= cmd[i]
		}
		cmd[12+n] = parity

		res, err := r.dev.SendCommand(cmd[:13+n], 128)
		if err != nil {
			lastErr = err
			continue
		}
		if res[0] != 0 || res[1] < 6 {
			lastErr = fmt.Errorf("B-CAS 応答エラー: status=%#02x len=%d", res[0], res[1])
			continue
		}
		respLen := int(res[6])
		if 7+respLen > len(res) {
			lastErr = fmt.Errorf("B-CAS 応答長超過: %d", respLen)
			continue
		}
		out := make([]byte, respLen)
		copy(out, res[7:7+respLen])
		return out, nil
	}
	return nil, fmt.Errorf("B-CAS 送信失敗（%d 回試行）: %w", r.retries+1, lastErr)
}
