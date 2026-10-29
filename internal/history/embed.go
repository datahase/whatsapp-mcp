package history

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/config"
)

var errLocalEmbedding = errors.New("local text embedding failed; verify the pinned model/runtime and resume indexing. Existing indexed messages are retained")

// Embedder never generates answers and must not send content to a hosted API.
type Embedder interface {
	Embed(context.Context, []string, string) ([][]float32, error)
	Manifest() Manifest
	Close() error
}
type Manifest struct {
	Model      string `json:"model"`
	Revision   string `json:"revision"`
	Dimensions int    `json:"dimensions"`
	Runtime    string `json:"runtime"`
	Device     string `json:"device"`
	Format     string `json:"format"`
}

func (m Manifest) Key() string {
	b, _ := json.Marshal(m)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

//go:embed worker.py
var workerSource []byte

const runtimeVersion = "sentence-transformers=6.2.0;transformers=5.21.0;torch=2.15.0;pillow=12.3.0;torchvision=0.29.1"
const formatVersion = "gemma2-search-question-document-l2-f32-chunk1200-overlap100-v1"

type LocalEmbedder struct {
	cfg      config.History
	dir      string
	manifest Manifest
	gate     chan struct{}
	mu       sync.Mutex
	cmd      *exec.Cmd
	in       io.WriteCloser
	out      *bufio.Scanner
}

func NewLocalEmbedder(c config.History, dir string) *LocalEmbedder {
	return &LocalEmbedder{cfg: c, dir: dir, manifest: Manifest{c.Model, c.Revision, c.Dimensions, runtimeVersion, c.Device, formatVersion}, gate: make(chan struct{}, 1)}
}
func (e *LocalEmbedder) Manifest() Manifest { return e.manifest }
func (e *LocalEmbedder) stop() {
	if e.cmd != nil {
		_ = e.cmd.Process.Kill()
		_ = e.cmd.Wait()
		e.cmd = nil
		e.in = nil
		e.out = nil
	}
}
func (e *LocalEmbedder) Close() error { e.mu.Lock(); defer e.mu.Unlock(); e.stop(); return nil }
func (e *LocalEmbedder) start() error {
	if e.cmd != nil {
		return nil
	}
	if err := os.MkdirAll(e.dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(e.dir, "embedding-worker.py")
	if err := os.WriteFile(path, workerSource, 0600); err != nil {
		return err
	}
	cache := e.cfg.CacheDir
	if cache == "" {
		cache = filepath.Join(e.dir, "models")
	}
	args := []string{"-u", path, "--model", e.cfg.Model, "--revision", e.cfg.Revision, "--dimensions", fmt.Sprint(e.cfg.Dimensions), "--device", e.cfg.Device, "--cache", cache}
	if e.cfg.Offline {
		args = append(args, "--offline")
	}
	cmd := exec.Command(e.cfg.Python, args...)
	// Do not pass provider keys, tokens or arbitrary process configuration to
	// the model worker. Only downloads of the pinned weights need networking.
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HF_HUB_DISABLE_TELEMETRY=1", "HF_HUB_DISABLE_IMPLICIT_TOKEN=1", "DO_NOT_TRACK=1", "PYTHONUNBUFFERED=1", "TOKENIZERS_PARALLELISM=false")
	var err error
	e.in, err = cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return errors.New("history embedding runtime unavailable; install the optional local history runtime")
	}
	e.cmd = cmd
	e.out = bufio.NewScanner(out)
	e.out.Buffer(make([]byte, 4096), 4<<20)
	return nil
}
func (e *LocalEmbedder) read(ctx context.Context) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	scanner := e.out
	go func() {
		if !scanner.Scan() {
			ch <- result{err: errors.New("local embedding worker stopped")}
			return
		}
		ch <- result{b: append([]byte(nil), scanner.Bytes()...)}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			e.stop()
		}
		return r.b, r.err
	case <-ctx.Done():
		e.stop()
		<-ch
		return nil, ctx.Err()
	}
}
func (e *LocalEmbedder) Embed(ctx context.Context, texts []string, task string) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > 16 {
		return nil, errors.New("embedding batch must contain 1-16 texts")
	}
	select {
	case e.gate <- struct{}{}:
		defer func() { <-e.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fresh := e.cmd == nil
	if err := e.start(); err != nil {
		return nil, err
	}
	if fresh {
		readyCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		b, err := e.read(readyCtx)
		if err != nil {
			return nil, err
		}
		var v struct {
			Ready   bool              `json:"ready"`
			Runtime map[string]string `json:"runtime"`
		}
		if json.Unmarshal(b, &v) != nil || !v.Ready || v.Runtime["sentence_transformers"] != "6.2.0" || v.Runtime["transformers"] != "5.21.0" || v.Runtime["torch"] != "2.15.0" || v.Runtime["pillow"] != "12.3.0" || v.Runtime["torchvision"] != "0.29.1" {
			e.stop()
			return nil, errors.New("local embedding model/runtime failed readiness; install pinned history requirements")
		}
	}
	b, _ := json.Marshal(map[string]any{"texts": texts, "task": task})
	b = append(b, '\n')
	if _, err := e.in.Write(b); err != nil {
		e.stop()
		return nil, errors.New("embedding worker write failed")
	}
	callCtx, callCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer callCancel()
	b, err := e.read(callCtx)
	if err != nil {
		return nil, err
	}
	var v struct {
		Vectors [][]float32 `json:"vectors"`
		Error   string      `json:"error"`
	}
	if json.Unmarshal(b, &v) != nil || v.Error != "" || len(v.Vectors) != len(texts) {
		return nil, errLocalEmbedding
	}
	for i := range v.Vectors {
		if err := normalize(v.Vectors[i], e.manifest.Dimensions); err != nil {
			return nil, err
		}
	}
	return v.Vectors, nil
}
func normalize(v []float32, n int) error {
	if len(v) != n {
		return errors.New("embedding dimension mismatch")
	}
	var norm float64
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return errors.New("embedding contains non-finite values")
		}
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return errors.New("embedding has zero norm")
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(norm))
	}
	return nil
}
func encodeVector(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}
func dot(b []byte, v []float32) float64 {
	if len(b) != len(v)*4 {
		return math.NaN()
	}
	var s float64
	for i, x := range v {
		value := math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return math.NaN()
		}
		s += float64(x) * float64(value)
	}
	return s
}
