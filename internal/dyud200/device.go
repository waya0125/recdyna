// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package dyud200 はダイナコネクティブ DY-UD200（1c11:1004）を Linux の usbfs 経由で制御する。
// プロトコルおよびコマンド列は有志の BonDriver_dyud v8.5.3 を参考に Go へ移植したもの。
package dyud200

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/waya0125/recdyna/internal/usbfs"
)

// DeviceID は DY-UD200 の USB VID/PID。
var DeviceID = usbfs.ID{Vendor: 0x1c11, Product: 0x1004}

const (
	epCmdOut uint8 = 0x02
	epResIn  uint8 = 0x84
	epTSIn   uint8 = 0x86

	bulkTimeoutMS uint32 = 2000

	// SegModeFull / SegModeOne はセグメントモード指定値。
	SegModeFull uint32 = 0x00020000
	SegModeOne  uint32 = 0x00010000
)

// ErrNoDevice は利用可能な DY-UD200 が見つからない場合のエラー。
var ErrNoDevice = errors.New("DY-UD200 が見つかりません")

// FirmwareVersion は DY-UD200 のファームウェアバージョン（major.minor1.minor2-minor3）。
type FirmwareVersion struct {
	Major  byte
	Minor1 byte
	Minor2 byte
	Minor3 byte
}

func (f FirmwareVersion) String() string {
	return fmt.Sprintf("%d.%d.%d-%d", f.Major, f.Minor1, f.Minor2, f.Minor3)
}

func (f FirmwareVersion) Uint32() uint32 {
	return uint32(f.Major)<<24 | uint32(f.Minor1)<<16 | uint32(f.Minor2)<<8 | uint32(f.Minor3)
}

// NeedsNewInit は新ファーム（1.0.5-2 / 1.0.5-3 等）用の再初期化シーケンスが必要かを返す。
func (f FirmwareVersion) NeedsNewInit() bool {
	return f.Major == 1 && f.Minor1 == 0 && f.Minor2 == 5 && f.Minor3 >= 2
}

// IsXorBypassed は TS の XOR 暗号化が無効化された既知カスタムファームウェア（1.0.5-3）かを返す。
func (f FirmwareVersion) IsXorBypassed() bool {
	return f.Uint32() == 0x01000503
}

// Device は開いた DY-UD200 チューナー。
type Device struct {
	USB    *usbfs.Device
	Log    io.Writer
	FW     FirmwareVersion
	Serial string

	ioMu   sync.Mutex
	cipher *cmdCipher

	xorMu      sync.RWMutex
	xorEnabled bool
	xorCipher  XORCipher
}

// OpenDevice は指定された機器ファイル（空なら最初に見つかった空き機器）を開く。
func OpenDevice(path string) (*Device, error) {
	var candidates []string
	if path != "" {
		candidates = []string{path}
	} else {
		found, err := usbfs.Find(DeviceID)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, ErrNoDevice
		}
		candidates = found
	}

	var lastErr error
	for _, p := range candidates {
		u, err := usbfs.Open(p)
		if err != nil {
			lastErr = err
			continue
		}
		if err := u.Claim(0); err != nil {
			u.Close()
			if errors.Is(err, syscall.EBUSY) && path == "" {
				lastErr = err
				continue
			}
			return nil, err
		}
		return &Device{
			USB:    u,
			Log:    io.Discard,
			cipher: newCmdCipher(),
		}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNoDevice
}

// SendCommand はコマンドを暗号化して送信し、応答を復号・CRC 検証して先頭 resLen バイトを返す。
func (d *Device) SendCommand(cmd []byte, resLen int) ([]byte, error) {
	d.ioMu.Lock()
	defer d.ioMu.Unlock()

	enc, err := d.cipher.EncryptCommand(cmd)
	if err != nil {
		return nil, err
	}
	n, err := d.USB.Bulk(epCmdOut, enc[:], bulkTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("コマンド送信失敗: %w", err)
	}
	if n != len(enc) {
		return nil, fmt.Errorf("コマンド送信長不足: %d/%d", n, len(enc))
	}

	var resEnc [128]byte
	var recvErr error
	for retry := range 10 {
		rn, err := d.USB.Bulk(epResIn, resEnc[:], bulkTimeoutMS)
		if err == nil && rn == len(resEnc) {
			recvErr = nil
			break
		}
		if err != nil {
			recvErr = err
		} else {
			recvErr = fmt.Errorf("応答受信長不足: %d/%d", rn, len(resEnc))
		}
		if retry < 9 {
			time.Sleep(time.Millisecond)
		}
	}
	if recvErr != nil {
		return nil, fmt.Errorf("応答受信失敗: %w", recvErr)
	}

	plain, err := d.cipher.DecryptResponse(resEnc[:])
	if err != nil {
		return nil, err
	}
	if resLen <= 0 || resLen > len(plain) {
		resLen = len(plain)
	}
	out := make([]byte, resLen)
	copy(out, plain[:resLen])
	return out, nil
}

var (
	cmdInit = []byte{
		0x0f, 0xf0, 0xe0, 0x20, 0x00, 0x00, 0x00, 0x00, 0x23, 0x8d, 0xc0, 0xd0, 0x0e, 0xd0, 0x38, 0x75,
		0xe8, 0x38, 0xdf, 0xeb, 0x33, 0x7d, 0x39, 0x52, 0xe3, 0x78, 0xeb, 0xd1, 0xb7, 0x84, 0xef, 0x65,
		0x00, 0x9a,
	}
	cmdInitNew = []byte{
		0x0f, 0xf0, 0xe0, 0x20, 0x00, 0x00, 0x00, 0x00, 0x7b, 0x36, 0x51, 0xe5, 0x21, 0x7c, 0x7b, 0x1c,
		0xe8, 0x38, 0xdf, 0xeb, 0x33, 0x7d, 0x39, 0x52, 0xe3, 0x78, 0xeb, 0xd1, 0xb7, 0x84, 0xef, 0x65,
		0x00, 0x00,
	}
	cmdInitNF = []byte{0x1f, 0xf0, 0xe3, 0x04, 0x00, 0x00, 0x00, 0x00}
	cmdClose  = []byte{0x1f, 0xf0, 0xe2, 0x04, 0x00, 0x00, 0x00, 0x00}
	cmdFWVer  = []byte{0x1f, 0xf0, 0x9f, 0x04, 0x00, 0x00, 0x00, 0x00}
	cmdSerial = []byte{0x1f, 0xf0, 0x95, 0x04, 0x00, 0x00, 0x00, 0x00}
	cmdLock   = []byte{0x1f, 0xf0, 0xb1, 0x04, 0x26, 0x0a, 0x00, 0x00}
	cmdSigLv  = []byte{0x1f, 0xf0, 0xd5, 0x04, 0x00, 0x00, 0x00, 0x00}
	cmdTSOn   = []byte{0x1f, 0xf0, 0xb4, 0x04, 0x00, 0x00, 0x00, 0x00}
	cmdTSOff  = []byte{0x1f, 0xf0, 0xb5, 0x04, 0x00, 0x00, 0x00, 0x00}
)

// Init は BonDriver_dyud の OpenTuner と同じ手順でデバイスを初期化する。
func (d *Device) Init() error {
	if _, err := d.SendCommand(cmdInit, 128); err != nil {
		return fmt.Errorf("初期化コマンド失敗: %w", err)
	}
	if err := d.StopTS(); err != nil {
		return err
	}
	if _, err := d.SendCommand(cmdInitNF, 2); err != nil {
		return fmt.Errorf("新FW初期化コマンド失敗: %w", err)
	}
	fv, err := d.GetFirmwareVersion()
	if err != nil {
		return fmt.Errorf("FWバージョン取得失敗: %w", err)
	}
	d.FW = fv
	fmt.Fprintf(d.Log, "DY-UD200 firmware: %s\n", fv)

	// FW 1.0.5-2 / 1.0.5-3 は新ファーム用の初期化列でやり直す
	if fv.NeedsNewInit() {
		fmt.Fprintf(d.Log, "FW %s detected: re-initializing with new init sequence\n", fv)
		if _, err := d.SendCommand(cmdClose, 2); err != nil {
			return fmt.Errorf("再初期化前クローズ失敗: %w", err)
		}
		if _, err := d.SendCommand(cmdInitNew, 128); err != nil {
			return fmt.Errorf("新FW再初期化失敗: %w", err)
		}
		if err := d.StopTS(); err != nil {
			return err
		}
		if _, err := d.SendCommand(cmdInitNF, 2); err != nil {
			return fmt.Errorf("新FW再初期化(e3)失敗: %w", err)
		}
	}

	serial, err := d.GetSerial()
	if err != nil {
		return fmt.Errorf("シリアル取得失敗: %w", err)
	}
	d.Serial = serial
	fmt.Fprintf(d.Log, "DY-UD200 serial: %s\n", serial)

	if err := d.SetSegMode(SegModeFull); err != nil {
		return fmt.Errorf("セグメント初期化失敗: %w", err)
	}
	return nil
}

// GetFirmwareVersion はファームウェアバージョンを取得する。
func (d *Device) GetFirmwareVersion() (FirmwareVersion, error) {
	res, err := d.SendCommand(cmdFWVer, 6)
	if err != nil {
		return FirmwareVersion{}, err
	}
	return FirmwareVersion{
		Major:  res[2],
		Minor1: res[3],
		Minor2: res[4],
		Minor3: res[5],
	}, nil
}

// GetSerial は本体のシリアル番号（最大 16 文字）を取得する。
func (d *Device) GetSerial() (string, error) {
	res, err := d.SendCommand(cmdSerial, 18)
	if err != nil {
		return "", err
	}
	return sanitizeSerial(res[2:18]), nil
}

func sanitizeSerial(raw []byte) string {
	buf := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b == 0 {
			break
		}
		if b >= 0x20 && b <= 0x7e {
			buf = append(buf, b)
		}
	}
	return strings.TrimSpace(string(buf))
}

// SetSegMode はフルセグ (SegModeFull) またはワンセグ (SegModeOne) を設定する。
func (d *Device) SetSegMode(mode uint32) error {
	cmd := [8]byte{0x1f, 0xf0, 0xd1, 0x04}
	binary.BigEndian.PutUint32(cmd[4:8], mode)
	res, err := d.SendCommand(cmd[:], 1)
	if err != nil {
		return err
	}
	if res[0] != 0 {
		return fmt.Errorf("SetSegMode エラー: %#02x", res[0])
	}
	return nil
}

// SetTSXorKey は TS の XOR セッション鍵を設定する。
func (d *Device) SetTSXorKey(key []byte) error {
	if len(key) > 32 {
		key = key[:32]
	}
	var cmd [36]byte
	cmd[0], cmd[1], cmd[2], cmd[3] = 0x1f, 0xf0, 0x30, 0x20

	var tmp XORCipher
	tmp.SetKey(defaultXORKey[:])
	tmp.Crypt(cmd[4:4+len(key)], key)

	res, err := d.SendCommand(cmd[:], 1)
	if err != nil {
		return fmt.Errorf("SetTSXorKey 失敗: %w", err)
	}
	if res[0] != 0 {
		return fmt.Errorf("SetTSXorKey エラー: %#02x", res[0])
	}

	d.xorMu.Lock()
	d.xorCipher.SetKey(key)
	d.xorEnabled = true
	d.xorMu.Unlock()
	return nil
}

// Tune は指定周波数（kHz）へ選局し、TS ストリーミングを開始する。
func (d *Device) Tune(freqKHz uint32, oneSeg bool, enableXOR bool) error {
	if err := d.StopTS(); err != nil {
		return err
	}
	seg := SegModeFull
	if oneSeg {
		seg = SegModeOne
	}
	if err := d.SetSegMode(seg); err != nil {
		return err
	}
	if enableXOR {
		if err := d.SetTSXorKey(sessionXORKey[:]); err != nil {
			return err
		}
	} else {
		d.xorMu.Lock()
		d.xorEnabled = false
		d.xorMu.Unlock()
	}

	cmd := [8]byte{0x1f, 0xf0, 0xd2, 0x06}
	binary.BigEndian.PutUint32(cmd[4:8], freqKHz)
	res, err := d.SendCommand(cmd[:], 1)
	if err != nil {
		return fmt.Errorf("周波数設定(%d kHz)失敗: %w", freqKHz, err)
	}
	if res[0] != 0 {
		return fmt.Errorf("周波数設定(%d kHz)エラー: %#02x", freqKHz, res[0])
	}
	fmt.Fprintf(d.Log, "tuned: %d kHz (oneseg=%v xor=%v)\n", freqKHz, oneSeg, enableXOR)
	return d.StartTS()
}

// GetLockStatus はチューナーのロック状態値（BonDriver_dyud では > 8 でロック済み）を返す。
func (d *Device) GetLockStatus() (byte, error) {
	res, err := d.SendCommand(cmdLock, 4)
	if err != nil {
		return 0, err
	}
	if res[0] != 0 {
		return 0, fmt.Errorf("GetLockStatus エラー: %#02x", res[0])
	}
	return res[3], nil
}

// WaitLock はチューナーがロックする（status > 8）まで最大 timeout 待機する。
func (d *Device) WaitLock(timeout time.Duration) (byte, bool) {
	deadline := time.Now().Add(timeout)
	var last byte
	for {
		st, err := d.GetLockStatus()
		if err == nil {
			last = st
			if st > 8 {
				return st, true
			}
		}
		if time.Now().After(deadline) {
			return last, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// GetSignalLevel は信号強度（BonDriver_dyud 換算値と生の 4 バイト値）を返す。
func (d *Device) GetSignalLevel() (float64, uint32, error) {
	res, err := d.SendCommand(cmdSigLv, 6)
	if err != nil {
		return 0, 0, err
	}
	if res[0] != 0 {
		return 0, 0, fmt.Errorf("GetSignalLevel エラー: %#02x", res[0])
	}
	raw := binary.BigEndian.Uint32(res[2:6])
	if res[2] == 0 {
		return 0, raw, nil
	}
	// BonDriver_dyud.cpp: ul = *(DWORD*)(szRes+2) & 0xffffff00; ul.GetInvert() / 65536.0 * 20.0
	val := uint32(res[3])<<16 | uint32(res[4])<<8 | uint32(res[5])
	return float64(val) / 65536.0 * 20.0, raw, nil
}

// StartTS は TS ストリーミングを開始する。
func (d *Device) StartTS() error {
	res, err := d.SendCommand(cmdTSOn, 1)
	if err != nil {
		return fmt.Errorf("StartTS 失敗: %w", err)
	}
	if res[0] != 0 {
		return fmt.Errorf("StartTS エラー: %#02x", res[0])
	}
	return nil
}

// StopTS は TS ストリーミングを停止する。
func (d *Device) StopTS() error {
	res, err := d.SendCommand(cmdTSOff, 1)
	if err != nil {
		return fmt.Errorf("StopTS 失敗: %w", err)
	}
	if res[0] != 0 {
		return fmt.Errorf("StopTS エラー: %#02x", res[0])
	}
	return nil
}

// StartStream は TS IN エンドポイント (0x86) から非同期 URB でバルク受信を始める。
func (d *Device) StartStream() (*usbfs.Stream, error) {
	// 24064 = 47 * 512 (USB High-Speed Bulk 最大パケット長の倍数) = 128 * 188 (TS パケット長の倍数)。
	return d.USB.StartStream(epTSIn, 16, 24064, 64)
}

// XORCipherCopy は現在設定されている XOR 復号器（有効な場合）を返す。
func (d *Device) XORCipherCopy() (*XORCipher, bool) {
	d.xorMu.RLock()
	defer d.xorMu.RUnlock()
	if !d.xorEnabled {
		return nil, false
	}
	cp := d.xorCipher
	return &cp, true
}

// Close は TS 停止とクローズコマンドを送ってから USB デバイスを閉じる。
func (d *Device) Close() error {
	if d.USB == nil {
		return nil
	}
	_ = d.StopTS()
	_, _ = d.SendCommand(cmdClose, 2)
	err := d.USB.Close()
	d.USB = nil
	return err
}

// Frequency はチャンネル番号を DY-UD200 の周波数（kHz）に変換する。
// UHF: 13〜62、CATV (catv=true): C13〜C63、VHF: 1〜12。
func Frequency(ch int, catv bool) (uint32, error) {
	if !catv {
		if ch >= 13 && ch <= 62 {
			return uint32(473143 + 6000*(ch-13)), nil
		}
		if ch >= 1 && ch <= 3 {
			return uint32(93143 + 6000*(ch-1)), nil
		}
		if ch >= 4 && ch <= 7 {
			return uint32(173143 + 6000*(ch-4)), nil
		}
		if ch >= 8 && ch <= 12 {
			return uint32(195143 + 6000*(ch-8)), nil
		}
		return 0, fmt.Errorf("未対応のチャンネル: %d（UHF 13〜62、VHF 1〜12、CATV C13〜C63）", ch)
	}
	switch {
	case ch >= 13 && ch <= 21:
		return uint32(111143 + 6000*(ch-13)), nil
	case ch == 22:
		return 167143, nil
	case ch == 23:
		return 225143, nil
	case ch >= 24 && ch <= 27:
		return uint32(233143 + 6000*(ch-24)), nil
	case ch >= 28 && ch <= 63:
		return uint32(255143 + 6000*(ch-28)), nil
	default:
		return 0, fmt.Errorf("未対応の CATV チャンネル: C%d（C13〜C63）", ch)
	}
}
