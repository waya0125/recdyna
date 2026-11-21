// このファイルは arib25（ARIB STD-B25 仕様確認テストプログラム、茂木 和洋 作）の ts_section_parser.c を
// Go に移したもの。arib25 の利用条件（LICENSES/arib25.txt）に同意して作った二次的著作物。
// 移植: waya0125（2026）。移植で足した部分は MIT。

package b25

import "errors"

// maxRawSectionSize はセクション 1 つの大きさの上限（arib25 と同じ）。
const maxRawSectionSize = 4100

var errInvalidPID = errors.New("セクションの PID が途中で変わった")

// section は組み立て中・組み立て済みのセクション。
type section struct {
	raw []byte
	// 見出しを読めたら data（見出しのあと）の位置が決まる。読めていなければ -1
	dataAt        int
	tableID       uint8
	syntax        bool // section_syntax_indicator
	sectionLength int
}

func newSection() *section { return &section{raw: make([]byte, 0, 64), dataAt: -1} }

// data は見出しのあと（CRC を含む）。
func (s *section) data() []byte { return s.raw[s.dataAt:] }

func (s *section) append(p []byte) {
	n := min(len(p), maxRawSectionSize-len(s.raw))
	s.raw = append(s.raw, p[:n]...)
	if s.dataAt < 0 {
		s.readHeader()
	}
	if s.dataAt < 0 {
		return
	}
	if total := s.sectionLength + 3; len(s.raw) > total {
		s.raw = s.raw[:total]
	}
}

func (s *section) readHeader() {
	s.dataAt = -1
	if len(s.raw) < 3 {
		return
	}
	p := s.raw
	s.tableID = p[0]
	s.syntax = p[1]&0x80 != 0
	s.sectionLength = int(p[1]&0x0f)<<8 | int(p[2])
	if !s.syntax {
		s.dataAt = 3
		return
	}
	if len(s.raw) < 8 {
		return
	}
	s.dataAt = 8
}

func (s *section) complete() bool {
	return s.dataAt >= 0 && len(s.raw) >= s.sectionLength+3
}

func (s *section) same(o *section) bool {
	if o == nil || len(s.raw) != len(o.raw) {
		return false
	}
	for i := range s.raw {
		if s.raw[i] != o.raw[i] {
			return false
		}
	}
	return true
}

// sectionParser は 1 つの PID のパケットからセクションを組み立てる。
// 前に出したものと同じ中身のセクションは出さない。
type sectionParser struct {
	pid  int
	work *section
	last *section
	buff []*section
}

func newSectionParser() *sectionParser { return &sectionParser{pid: -1} }

// put は TS パケットのペイロードを渡す。
func (sp *sectionParser) put(pid int, unitStart bool, data []byte) error {
	if len(data) < 1 {
		return errors.New("セクションのデータが空")
	}
	if sp.pid >= 0 && sp.pid != pid {
		return errInvalidPID
	}
	sp.pid = pid
	if !unitStart {
		sp.putContinuation(data)
		return nil
	}
	sp.putStart(data)
	return nil
}

func (sp *sectionParser) finish(s *section) {
	if s.syntax && crc32(s.raw) != 0 {
		return // CRC が合わない
	}
	if s.same(sp.last) {
		return
	}
	sp.last = s
	sp.buff = append(sp.buff, s)
}

func (sp *sectionParser) putContinuation(data []byte) {
	w := sp.work
	if w == nil || len(w.raw) == 0 {
		return
	}
	w.append(data)
	if !w.complete() {
		return
	}
	sp.work = nil
	sp.finish(w)
}

func (sp *sectionParser) putStart(data []byte) {
	pointer := int(data[0])
	p := data[1:]
	if pointer >= len(p) {
		// 壊れている
		sp.work = nil
		return
	}
	if pointer > 0 {
		sp.putContinuation(p[:pointer])
		p = p[pointer:]
	}
	sp.work = nil
	for {
		w := newSection()
		w.append(p)
		if !w.complete() {
			sp.work = w
			return
		}
		length := len(w.raw)
		sp.finish(w)
		p = p[length:]
		if len(p) == 0 || p[0] == 0xff {
			return
		}
	}
}

// count は取り出せるセクションの数。
func (sp *sectionParser) count() int { return len(sp.buff) }

// get は組み立て済みのセクションを 1 つ取り出す。
func (sp *sectionParser) get() *section {
	if len(sp.buff) == 0 {
		return nil
	}
	s := sp.buff[0]
	sp.buff = sp.buff[1:]
	return s
}

var crcTable = func() (t [256]uint32) {
	for i := range t {
		c := uint32(i) << 24
		for range 8 {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return
}()

// crc32 は MPEG-2 の CRC32。CRC まで含めて計算すると 0 になる。
func crc32(p []byte) uint32 {
	crc := uint32(0xffffffff)
	for _, b := range p {
		crc = crc<<8 ^ crcTable[(crc>>24^uint32(b))&0xff]
	}
	return crc
}
