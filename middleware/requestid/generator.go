package requestid

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	mathrand "math/rand"
	"sync"
	"sync/atomic"
	"time"
)

const (
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

	// Custom epoch to reduce timestamp bits: 2020-01-01T00:00:00Z
	epochMilli int64 = 1577836800000

	tsBits   = 40
	randBits = 14
	seqBits  = 10

	seqMax   = (1 << seqBits) - 1
	randMask = (1 << randBits) - 1
	randMax  = randMask + 1

	shiftRand = seqBits
	shiftTS   = randBits + seqBits

	idWidth = 12

	randomPoolSize = 4096
)

var errInvalidBase62 = errors.New("invalid base62 request id")

// RequestIDGenerator encapsulates state for request ID generation.
type RequestIDGenerator struct {
	initialize sync.Once
	rng        *mathrand.Rand
	stateMu    sync.Mutex
	lastMs     int64
	seq        int32
	randomPool []int32
	poolIdx    atomic.Int32
	poolMu     sync.RWMutex
}

func cryptoSeed() int64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return time.Now().UnixNano()
	}
	return int64(binary.LittleEndian.Uint64(buf[:]))
}

// NewRequestIDGenerator creates a request ID generator with a pre-generated random pool.
func NewRequestIDGenerator() *RequestIDGenerator {
	g := &RequestIDGenerator{
		rng:        mathrand.New(mathrand.NewSource(cryptoSeed())),
		randomPool: make([]int32, randomPoolSize),
	}
	for i := range g.randomPool {
		g.randomPool[i] = int32(g.rng.Intn(randMax))
	}
	return g
}

var packageGenerator = NewRequestIDGenerator()

// NewRequestID generates a fixed-width base62 correlation ID using the package
// default generator. It is not a secret, token, nonce, or authorization value:
// the default format carries a decodable timestamp component for operational
// ordering. Applications that need explicit generation policy should create a
// RequestIDGenerator and pass its Generate method through WithGenerator.
func NewRequestID() string {
	return packageGenerator.Generate()
}

// Generate creates a new request ID with monotonic timestamp/sequence coordination.
func (g *RequestIDGenerator) Generate() string {
	g.ensureInitialized()
	for {
		nowMs := time.Now().UnixMilli()
		g.stateMu.Lock()
		if nowMs < g.lastMs {
			nowMs = g.lastMs
		}

		if nowMs == g.lastMs {
			if g.seq < int32(seqMax) {
				g.seq++
				seq := g.seq
				g.stateMu.Unlock()
				return g.buildID(nowMs, int(seq))
			}
			g.stateMu.Unlock()
			time.Sleep(50 * time.Microsecond)
			continue
		}

		g.lastMs = nowMs
		g.seq = 0
		g.stateMu.Unlock()
		return g.buildID(nowMs, 0)
	}
}

func (g *RequestIDGenerator) buildID(unixMs int64, seqVal int) string {
	r := g.randomValue()
	delta := unixMs - epochMilli
	if delta < 0 {
		delta = 0
	}

	ts := uint64(delta) & ((uint64(1) << tsBits) - 1)
	v := (ts << shiftTS) | (uint64(r&randMask) << shiftRand) | uint64(seqVal)

	return encodeBase62Fixed(v, idWidth)
}

func (g *RequestIDGenerator) ensureInitialized() {
	g.initialize.Do(func() {
		g.poolMu.Lock()
		defer g.poolMu.Unlock()
		if g.rng != nil && len(g.randomPool) > 0 {
			return
		}
		if g.rng == nil {
			g.rng = mathrand.New(mathrand.NewSource(cryptoSeed()))
		}
		if len(g.randomPool) == 0 {
			g.randomPool = make([]int32, randomPoolSize)
		}
		for i := range g.randomPool {
			g.randomPool[i] = int32(g.rng.Intn(randMax))
		}
	})
}

func (g *RequestIDGenerator) randomValue() int {
	g.ensureInitialized()

	g.poolMu.RLock()
	poolSize := len(g.randomPool)
	g.poolMu.RUnlock()
	if poolSize == 0 {
		return g.randomFallback()
	}

	idx := uint32(g.poolIdx.Add(1) - 1)
	pos := int(idx % uint32(poolSize))

	if pos == 0 && idx != 0 {
		g.refreshRandomPool()
	}

	g.poolMu.RLock()
	v := int(g.randomPool[pos])
	g.poolMu.RUnlock()
	return v
}

func (g *RequestIDGenerator) refreshRandomPool() {
	g.poolMu.Lock()
	defer g.poolMu.Unlock()

	if g.rng == nil {
		g.rng = mathrand.New(mathrand.NewSource(cryptoSeed()))
	}
	for i := range g.randomPool {
		g.randomPool[i] = int32(g.rng.Intn(randMax))
	}
}

func (g *RequestIDGenerator) randomFallback() int {
	g.poolMu.Lock()
	defer g.poolMu.Unlock()

	if g.rng == nil {
		g.rng = mathrand.New(mathrand.NewSource(cryptoSeed()))
	}
	return g.rng.Intn(randMax)
}

// DecodeRequestID reverses a fixed-width base62 request ID into components.
// This is provided for diagnostics and demonstrates why generated request IDs
// must be treated as correlation identifiers rather than secrets.
func DecodeRequestID(id string) (unixMilli int64, r int, seqVal int, err error) {
	if len(id) != idWidth {
		return 0, 0, 0, errInvalidBase62
	}

	v, err := decodeBase62(id)
	if err != nil {
		return 0, 0, 0, err
	}

	seqVal = int(v & uint64(seqMax))
	r = int((v >> shiftRand) & uint64(randMask))
	ts := (v >> shiftTS) & ((uint64(1) << tsBits) - 1)

	unixMilli = int64(ts) + epochMilli
	return unixMilli, r, seqVal, nil
}

func encodeBase62Fixed(v uint64, width int) string {
	if width <= 0 {
		width = 1
	}

	buf := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		buf[i] = base62Alphabet[int(v%62)]
		v /= 62
	}
	return string(buf)
}

func decodeBase62(id string) (uint64, error) {
	var v uint64
	for i := 0; i < len(id); i++ {
		ch := id[i]
		var digit int
		switch {
		case ch >= '0' && ch <= '9':
			digit = int(ch - '0')
		case ch >= 'A' && ch <= 'Z':
			digit = int(ch-'A') + 10
		case ch >= 'a' && ch <= 'z':
			digit = int(ch-'a') + 36
		default:
			return 0, errInvalidBase62
		}
		v = v*62 + uint64(digit)
	}
	return v, nil
}
