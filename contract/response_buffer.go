package contract

import (
	"bytes"
	"sync"
)

const maxPooledJSONBufferCapacity = 64 << 10

var jsonBufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 4096))
	},
}

func getJSONBuffer() *bytes.Buffer {
	return jsonBufferPool.Get().(*bytes.Buffer)
}

func putJSONBuffer(buf *bytes.Buffer) {
	if buf == nil || buf.Cap() > maxPooledJSONBufferCapacity {
		return
	}
	buf.Reset()
	jsonBufferPool.Put(buf)
}
