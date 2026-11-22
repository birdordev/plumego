package kvengine

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	// WAL entry format:
	// [Magic:4][Op:1][KeyLen:4][Key:var][ValueLen:4][Value:var][ExpireAt:8][Version:8][CRC:4]
	walEntryMagic = 0x57414C45                // "WALE"
	walHeaderSize = 4 + 1 + 4 + 4 + 8 + 8 + 4 // 33 bytes fixed overhead

	// Snapshot entry format:
	// [KeyLen:4][Key:var][ValueLen:4][Value:var][ExpireAt:8][Version:8][Size:8]
	snapshotHeaderSize = 4 + 4 + 8 + 8 + 8 // 32 bytes fixed overhead

	// Safety limits to prevent overflow and excessive memory allocation
	maxKeySize   = 64 * 1024         // 64KB max key size
	maxValueSize = 512 * 1024 * 1024 // 512MB max value size
)

// BinarySerializer implements Serializer using custom binary encoding
type BinarySerializer struct{}

// EncodeWALEntry encodes a WAL entry to binary format with zero allocations
func (s *BinarySerializer) EncodeWALEntry(entry WALEntry) ([]byte, error) {
	keyLen := len(entry.Key)
	valueLen := len(entry.Value)

	// Validate sizes to prevent overflow
	if keyLen > maxKeySize {
		return nil, fmt.Errorf("key size %d exceeds maximum %d", keyLen, maxKeySize)
	}
	if valueLen > maxValueSize {
		return nil, fmt.Errorf("value size %d exceeds maximum %d", valueLen, maxValueSize)
	}

	// Guard against integer overflow when computing totalSize
	if keyLen > math.MaxInt-walHeaderSize {
		return nil, fmt.Errorf("key size too large")
	}
	// At this point walHeaderSize + keyLen is guaranteed not to overflow.
	sum := walHeaderSize + keyLen
	if valueLen > math.MaxInt-sum {
		return nil, fmt.Errorf("value size too large")
	}

	totalSize := sum + valueLen
	// Defensive check: ensure totalSize is a valid, non-overflowed int for allocation.
	if totalSize < 0 || totalSize > math.MaxInt {
		return nil, fmt.Errorf("entry too large")
	}

	buf := make([]byte, totalSize)
	offset := 0

	// Write magic number
	binary.BigEndian.PutUint32(buf[offset:], walEntryMagic)
	offset += 4

	// Write op
	buf[offset] = entry.Op
	offset += 1

	// Write key length and key
	binary.BigEndian.PutUint32(buf[offset:], uint32(keyLen))
	offset += 4
	copy(buf[offset:], entry.Key)
	offset += keyLen

	// Write value length and value
	binary.BigEndian.PutUint32(buf[offset:], uint32(valueLen))
	offset += 4
	copy(buf[offset:], entry.Value)
	offset += valueLen

	// Write expire time (Unix nanoseconds)
	var expireNano int64
	if !entry.ExpireAt.IsZero() {
		expireNano = entry.ExpireAt.UnixNano()
	}
	binary.BigEndian.PutUint64(buf[offset:], uint64(expireNano))
	offset += 8

	// Write version
	binary.BigEndian.PutUint64(buf[offset:], uint64(entry.Version))
	offset += 8

	// Write CRC
	binary.BigEndian.PutUint32(buf[offset:], entry.CRC)

	return buf, nil
}

// DecodeWALEntry decodes a WAL entry from binary format
func (s *BinarySerializer) DecodeWALEntry(reader *bufio.Reader) (*WALEntry, error) {
	// Read magic number
	var scratch [8]byte
	magicBuf := scratch[:4]
	if _, err := io.ReadFull(reader, magicBuf); err != nil {
		return nil, err
	}
	magic := binary.BigEndian.Uint32(magicBuf)
	if magic != walEntryMagic {
		return nil, fmt.Errorf("invalid WAL entry magic: 0x%X", magic)
	}

	// Read op
	opBuf := scratch[:1]
	if _, err := io.ReadFull(reader, opBuf); err != nil {
		return nil, err
	}
	op := opBuf[0]

	// Read key length
	keyLenBuf := scratch[:4]
	if _, err := io.ReadFull(reader, keyLenBuf); err != nil {
		return nil, err
	}
	keyLen := binary.BigEndian.Uint32(keyLenBuf)

	// Validate key length
	if keyLen > maxKeySize {
		return nil, fmt.Errorf("key length %d exceeds maximum %d", keyLen, maxKeySize)
	}

	// Read key
	keyBuf := make([]byte, keyLen)
	if _, err := io.ReadFull(reader, keyBuf); err != nil {
		return nil, err
	}
	key := string(keyBuf)

	// Read value length
	valueLenBuf := scratch[:4]
	if _, err := io.ReadFull(reader, valueLenBuf); err != nil {
		return nil, err
	}
	valueLen := binary.BigEndian.Uint32(valueLenBuf)

	// Validate value length
	if valueLen > maxValueSize {
		return nil, fmt.Errorf("value length %d exceeds maximum %d", valueLen, maxValueSize)
	}

	// Read value
	value := make([]byte, valueLen)
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, err
	}

	// Read expire time
	expireBuf := scratch[:8]
	if _, err := io.ReadFull(reader, expireBuf); err != nil {
		return nil, err
	}
	expireNano := int64(binary.BigEndian.Uint64(expireBuf))
	var expireAt time.Time
	if expireNano > 0 {
		expireAt = time.Unix(0, expireNano)
	}

	// Read version
	versionBuf := scratch[:8]
	if _, err := io.ReadFull(reader, versionBuf); err != nil {
		return nil, err
	}
	version := int64(binary.BigEndian.Uint64(versionBuf))

	// Read CRC
	crcBuf := scratch[:4]
	if _, err := io.ReadFull(reader, crcBuf); err != nil {
		return nil, err
	}
	crc := binary.BigEndian.Uint32(crcBuf)

	return &WALEntry{
		Op:       op,
		Key:      key,
		Value:    value,
		ExpireAt: expireAt,
		Version:  version,
		CRC:      crc,
	}, nil
}

// WriteSnapshotHeader writes the binary snapshot header
func (s *BinarySerializer) WriteSnapshotHeader(writer io.Writer) error {
	// Write magic number
	if err := binary.Write(writer, binary.BigEndian, magicNumber); err != nil {
		return err
	}

	// Write version
	if err := binary.Write(writer, binary.BigEndian, version); err != nil {
		return err
	}

	// Write timestamp (Unix nanoseconds)
	timestamp := time.Now().UnixNano()
	if err := binary.Write(writer, binary.BigEndian, timestamp); err != nil {
		return err
	}

	return nil
}

// ReadSnapshotHeader reads and validates the binary snapshot header
func (s *BinarySerializer) ReadSnapshotHeader(reader io.Reader) error {
	// Read magic number
	var magic uint32
	if err := binary.Read(reader, binary.BigEndian, &magic); err != nil {
		return err
	}
	if magic != magicNumber {
		return fmt.Errorf("invalid snapshot magic: 0x%X", magic)
	}

	// Read version
	var ver uint32
	if err := binary.Read(reader, binary.BigEndian, &ver); err != nil {
		return err
	}
	if ver != version {
		return fmt.Errorf("unsupported snapshot version: %d", ver)
	}

	// Read timestamp (we don't use it, but need to consume it)
	var timestamp int64
	if err := binary.Read(reader, binary.BigEndian, &timestamp); err != nil {
		return err
	}

	return nil
}

// EncodeEntry encodes a snapshot entry to binary format
func (s *BinarySerializer) EncodeEntry(entry *Entry) ([]byte, error) {
	keyLen := len(entry.Key)
	valueLen := len(entry.Value)

	// Validate sizes to prevent overflow
	if keyLen > maxKeySize {
		return nil, fmt.Errorf("key size %d exceeds maximum %d", keyLen, maxKeySize)
	}
	if valueLen > maxValueSize {
		return nil, fmt.Errorf("value size %d exceeds maximum %d", valueLen, maxValueSize)
	}

	// Safe calculation: all values are now bounded
	totalSize := snapshotHeaderSize + keyLen + valueLen
	if totalSize < 0 {
		return nil, fmt.Errorf("total size overflow: %d", totalSize)
	}

	buf := make([]byte, totalSize)
	offset := 0

	// Write key length and key
	binary.BigEndian.PutUint32(buf[offset:], uint32(keyLen))
	offset += 4
	copy(buf[offset:], entry.Key)
	offset += keyLen

	// Write value length and value
	binary.BigEndian.PutUint32(buf[offset:], uint32(valueLen))
	offset += 4
	copy(buf[offset:], entry.Value)
	offset += valueLen

	// Write expire time (Unix nanoseconds)
	var expireNano int64
	if !entry.ExpireAt.IsZero() {
		expireNano = entry.ExpireAt.UnixNano()
	}
	binary.BigEndian.PutUint64(buf[offset:], uint64(expireNano))
	offset += 8

	// Write version
	binary.BigEndian.PutUint64(buf[offset:], uint64(entry.Version))
	offset += 8

	// Write size
	binary.BigEndian.PutUint64(buf[offset:], uint64(entry.Size))

	return buf, nil
}

// DecodeEntry decodes a snapshot entry from binary format
func (s *BinarySerializer) DecodeEntry(reader *bufio.Reader) (*Entry, error) {
	// Read key length
	keyLenBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, keyLenBuf); err != nil {
		return nil, err
	}
	keyLen := binary.BigEndian.Uint32(keyLenBuf)

	// Validate key length
	if keyLen > maxKeySize {
		return nil, fmt.Errorf("key length %d exceeds maximum %d", keyLen, maxKeySize)
	}

	// Read key
	keyBuf := make([]byte, keyLen)
	if _, err := io.ReadFull(reader, keyBuf); err != nil {
		return nil, err
	}
	key := string(keyBuf)

	// Read value length
	valueLenBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, valueLenBuf); err != nil {
		return nil, err
	}
	valueLen := binary.BigEndian.Uint32(valueLenBuf)

	// Validate value length
	if valueLen > maxValueSize {
		return nil, fmt.Errorf("value length %d exceeds maximum %d", valueLen, maxValueSize)
	}

	// Read value
	value := make([]byte, valueLen)
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, err
	}

	// Read expire time
	expireBuf := make([]byte, 8)
	if _, err := io.ReadFull(reader, expireBuf); err != nil {
		return nil, err
	}
	expireNano := int64(binary.BigEndian.Uint64(expireBuf))
	var expireAt time.Time
	if expireNano > 0 {
		expireAt = time.Unix(0, expireNano)
	}

	// Read version
	versionBuf := make([]byte, 8)
	if _, err := io.ReadFull(reader, versionBuf); err != nil {
		return nil, err
	}
	version := int64(binary.BigEndian.Uint64(versionBuf))

	// Read size
	sizeBuf := make([]byte, 8)
	if _, err := io.ReadFull(reader, sizeBuf); err != nil {
		return nil, err
	}
	size := int64(binary.BigEndian.Uint64(sizeBuf))

	return &Entry{
		Key:      key,
		Value:    value,
		ExpireAt: expireAt,
		Version:  version,
		Size:     size,
	}, nil
}

// Format returns the serialization format
func (s *BinarySerializer) Format() SerializationFormat {
	return FormatBinary
}
