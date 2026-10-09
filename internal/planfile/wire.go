package planfile

import (
	"errors"
	"fmt"
)

type wireType int

const (
	wireVarint  wireType = 0
	wireFixed64 wireType = 1
	wireBytes   wireType = 2
	wireFixed32 wireType = 5
)

// pbField is one decoded protobuf field.
type pbField struct {
	num  int
	wire wireType
	v    uint64 // value when wire == wireVarint
	b    []byte // payload when wire == wireBytes
}

// parseFields walks the protobuf wire format of a single message.
func parseFields(data []byte) ([]pbField, error) {
	var fields []pbField
	for len(data) > 0 {
		key, n := consumeVarint(data)
		if n <= 0 {
			return nil, errors.New("planfile: invalid varint key")
		}
		data = data[n:]
		f := pbField{num: int(key >> 3), wire: wireType(key & 7)}
		switch f.wire {
		case wireVarint:
			v, n := consumeVarint(data)
			if n <= 0 {
				return nil, errors.New("planfile: invalid varint value")
			}
			f.v = v
			data = data[n:]
		case wireFixed64:
			if len(data) < 8 {
				return nil, errors.New("planfile: truncated fixed64")
			}
			data = data[8:]
		case wireBytes:
			l, n := consumeVarint(data)
			if n <= 0 {
				return nil, errors.New("planfile: invalid length prefix")
			}
			data = data[n:]
			if uint64(len(data)) < l {
				return nil, errors.New("planfile: truncated bytes field")
			}
			f.b = data[:l]
			data = data[l:]
		case wireFixed32:
			if len(data) < 4 {
				return nil, errors.New("planfile: truncated fixed32")
			}
			data = data[4:]
		default:
			return nil, fmt.Errorf("planfile: unsupported wire type %d", f.wire)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func consumeVarint(b []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i, c := range b {
		if i >= 10 {
			return 0, -1
		}
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, -1
}
