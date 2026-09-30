// Package nodestore 是从节点本机的日志和记录存储（设计第 12 节：日志和记录留在从节点，由从节点提供查询）。
//
// 按"种类/日期"分段的 JSON 行文件（<目录>/<种类>/<YYYYMMDD>.jsonl），追加写、重启不丢；按保留期整天删除，
// 总大小超过上限时先删最旧的。查询接口（后台按节点查，WP14）建在 Scan 之上。
package nodestore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultMaxBytes 是默认的总大小上限。
const DefaultMaxBytes int64 = 2 << 30

// maxLineBytes 是单条记录的上限（超过的不写，记错误）。
const maxLineBytes = 1 << 20

var kindPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Options 是存储选项。
type Options struct {
	// MaxBytes 是所有种类加起来的上限；0 用 DefaultMaxBytes。
	MaxBytes int64
	// Now 是时钟（测试用）；nil 用 time.Now。
	Now func() time.Time
}

// Store 是本机存储。并发安全。
type Store struct {
	dir      string
	maxBytes int64
	now      func() time.Time

	mu    sync.Mutex
	open  map[string]*segment // 种类 -> 今天的段
	total int64
}

type segment struct {
	day  string
	file *os.File
	w    *bufio.Writer
}

// Open 打开（必要时创建）存储目录。
func Open(dir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("nodestore: %w", err)
	}
	s := &Store{dir: dir, maxBytes: opts.MaxBytes, now: opts.Now, open: map[string]*segment{}}
	if s.maxBytes <= 0 {
		s.maxBytes = DefaultMaxBytes
	}
	if s.now == nil {
		s.now = time.Now
	}
	files, err := s.allFiles()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		s.total += f.size
	}
	return s, nil
}

func dayOf(t time.Time) string { return t.UTC().Format("20060102") }

// Append 追加一条记录（JSON）。写进今天的段，并落盘（记录量小，每条都 flush）。
func (s *Store) Append(kind string, rec any) error {
	if !kindPattern.MatchString(kind) {
		return fmt.Errorf("nodestore: invalid kind %q", kind)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(line) > maxLineBytes {
		return fmt.Errorf("nodestore: record of %d bytes is too large", len(line))
	}
	line = append(line, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	seg, err := s.segmentLocked(kind)
	if err != nil {
		return err
	}
	if _, err := seg.w.Write(line); err != nil {
		return err
	}
	if err := seg.w.Flush(); err != nil {
		return err
	}
	s.total += int64(len(line))
	if s.total > s.maxBytes {
		s.enforceLimitLocked()
	}
	return nil
}

func (s *Store) segmentLocked(kind string) (*segment, error) {
	day := dayOf(s.now())
	if seg := s.open[kind]; seg != nil {
		if seg.day == day {
			return seg, nil
		}
		_ = seg.w.Flush()
		_ = seg.file.Close()
		delete(s.open, kind)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, kind), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, kind, day+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	seg := &segment{day: day, file: f, w: bufio.NewWriter(f)}
	s.open[kind] = seg
	return seg, nil
}

type fileInfo struct {
	kind, day, path string
	size            int64
}

func (s *Store) allFiles() ([]fileInfo, error) {
	kinds, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []fileInfo
	for _, k := range kinds {
		if !k.IsDir() {
			continue
		}
		files, err := s.kindFiles(k.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, files...)
	}
	return out, nil
}

func (s *Store) kindFiles(kind string) ([]fileInfo, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, kind))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []fileInfo
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fileInfo{kind: kind, day: strings.TrimSuffix(name, ".jsonl"), path: filepath.Join(s.dir, kind, name), size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].day < out[j].day })
	return out, nil
}

// enforceLimitLocked 删掉最旧的段，直到总大小回到上限的九成以下（今天正在写的段不删）。
func (s *Store) enforceLimitLocked() {
	files, err := s.allFiles()
	if err != nil {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].day < files[j].day })
	target := s.maxBytes / 10 * 9
	for _, f := range files {
		if s.total <= target {
			return
		}
		if seg := s.open[f.kind]; seg != nil && seg.day == f.day {
			continue
		}
		if err := os.Remove(f.path); err == nil {
			s.total -= f.size
		}
	}
}

// DeleteBefore 删掉某个种类里整天都早于 cutoff 的段（按天，保留期最多多留不到一天），返回删掉的记录条数。
func (s *Store) DeleteBefore(kind string, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.kindFiles(kind)
	if err != nil {
		return 0, err
	}
	cut := dayOf(cutoff)
	var deleted int64
	for _, f := range files {
		if f.day >= cut {
			break
		}
		n, _ := countLines(f.path)
		if seg := s.open[kind]; seg != nil && seg.day == f.day {
			_ = seg.file.Close()
			delete(s.open, kind)
		}
		if err := os.Remove(f.path); err != nil {
			return deleted, err
		}
		s.total -= f.size
		deleted += n
	}
	return deleted, nil
}

func countLines(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return int64(bytes.Count(b, []byte{'\n'})), nil
}

// Scan 按时间从新到旧逐条交给 fn（fn 返回 false 时停）；只看 [from, to] 这些天的段（零值表示不限）。
// 记录是原始 JSON，由调用方解码和过滤。
func (s *Store) Scan(kind string, from, to time.Time, fn func(line []byte) bool) error {
	s.mu.Lock()
	if seg := s.open[kind]; seg != nil {
		_ = seg.w.Flush()
	}
	files, err := s.kindFiles(kind)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		if !from.IsZero() && f.day < dayOf(from) {
			break
		}
		if !to.IsZero() && f.day > dayOf(to) {
			continue
		}
		b, err := os.ReadFile(f.path)
		if err != nil {
			return err
		}
		lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte{'\n'})
		for j := len(lines) - 1; j >= 0; j-- {
			if len(lines[j]) == 0 {
				continue
			}
			if !fn(lines[j]) {
				return nil
			}
		}
	}
	return nil
}

// Close 关闭打开的段。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for kind, seg := range s.open {
		_ = seg.w.Flush()
		_ = seg.file.Close()
		delete(s.open, kind)
	}
	return nil
}
