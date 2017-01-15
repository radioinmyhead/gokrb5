package keytab

//Ref: https://web.mit.edu/kerberos/krb5-devel/doc/formats/keytab_file_format.html

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/ioutil"
	"time"
	"unsafe"
)

type Keytab struct {
	Version uint16
	Entries []KeytabEntry
}

type KeytabEntry struct {
	Principal Principal
	Timestamp time.Time
	KVNO8     uint8
	Key       KeyBlock
	KVNO      uint32
}

type Principal struct {
	NumComponents int16
	Realm         string
	Components    []string
	NameType      int32
}

type KeyBlock struct {
	EType       uint16
	KeyMaterial []byte
}

func NewKeytab() Keytab {
	var e []KeytabEntry
	return Keytab{
		Version: 0,
		Entries: e,
	}
}

func newKeytabEntry() KeytabEntry {
	return KeytabEntry{
		Principal: newPrincipal(),
		Timestamp: time.Time{},
		KVNO8:     0,
		Key:       newKeyBlock(),
		KVNO:      0,
	}
}

func newPrincipal() Principal {
	var c []string
	return Principal{
		NumComponents: 0,
		Realm:         "",
		Components:    c,
		NameType:      0,
	}
}

func newKeyBlock() KeyBlock {
	var b []byte
	return KeyBlock{
		EType:       0,
		KeyMaterial: b,
	}
}

func Load(ktPath string) (kt Keytab, err error) {
	k, err := ioutil.ReadFile(ktPath)
	if err != nil {
		return
	}
	return Parse(k)
}

func Parse(b []byte) (kt Keytab, err error) {
	//The first byte of the file always has the value 5
	if int8(b[0]) != 5 {
		err = errors.New("Invalid keytab data. First byte does not equal 5")
		return
	}
	//Get keytab version
	//The second byte contains the version number (1 or 2)
	kt.Version = uint16(b[1])
	if kt.Version != 1 && kt.Version != 2 {
		err = errors.New("Invalid keytab data. Keytab version is neither 1 nor 2")
		return
	}
	//Version 1 of the file format uses native byte order for integer representations. Version 2 always uses big-endian byte order
	var endian binary.ByteOrder
	endian = binary.BigEndian
	if kt.Version == 1 && IsNativeEndianLittle() {
		endian = binary.LittleEndian
	}
	/*
		After the two-byte version indicator, the file contains a sequence of signed 32-bit record lengths followed by key records or holes.
		A positive record length indicates a valid key entry whose size is equal to or less than the record length.
		A negative length indicates a zero-filled hole whose size is the inverse of the length.
		A length of 0 indicates the end of the file.
	*/
	// n tracks position in the byte array
	n := 2
	l := read_int32(b, &n, &endian)
	for l != 0 {
		if l < 0 {
			//Zero padded so skip over
			l = l * -1
			n = n + int(l)
		}
		if l > 0 {
			//fmt.Printf("Bytes for entry: %v\n", b[p:p+int(l)])
			eb := b[n : n+int(l)]
			n = n + int(l)
			ke := newKeytabEntry()
			// p keeps track as to where we are in the byte stream
			var p int
			parse_principal(eb, &p, &kt, &ke, &endian)
			ke.Timestamp = read_timestamp(eb, &p, &endian)
			ke.KVNO8 = uint8(read_int8(eb, &p, &endian))
			ke.Key.EType = uint16(read_int16(eb, &p, &endian))
			key_len := int(read_int16(eb, &p, &endian))
			ke.Key.KeyMaterial = read_Bytes(eb, &p, key_len, &endian)
			//The 32-bit key version overrides the 8-bit key version.
			// To determine if it is present, the implementation must check that at least 4 bytes remain in the record after the other fields are read,
			// and that the value of the 32-bit integer contained in those bytes is non-zero.
			if len(eb)-p >= 4 {
				// The 32-bit key may be present
				ke.KVNO = uint32(read_int32(eb, &p, &endian))
			}
			if ke.KVNO == 0 {
				// Handles if the value from the last 4 bytes was zero and also if there are not the 4 bytes present. Makes sense to put the same value here as KVNO8
				ke.KVNO = uint32(ke.KVNO8)
			}
			// Add the entry to the keytab
			kt.Entries = append(kt.Entries, ke)
		}
		// Check if there are still 4 bytes left to read
		if len(b[n:]) < 4 {
			break
		}
		// Read the size of the next entry
		l = read_int32(b, &n, &endian)
	}
	return
}

func parse_principal(b []byte, p *int, kt *Keytab, ke *KeytabEntry, e *binary.ByteOrder) (err error) {
	ke.Principal.NumComponents = read_int16(b, p, e)
	if kt.Version == 1 {
		//In version 1 the number of components includes the realm. Minus 1 to make consistent with version 2
		ke.Principal.NumComponents -= 1
	}
	len_realm := read_int16(b, p, e)
	ke.Principal.Realm = string(read_Bytes(b, p, int(len_realm), e))
	for i := 0; i < int(ke.Principal.NumComponents); i++ {
		l := read_int16(b, p, e)
		ke.Principal.Components = append(ke.Principal.Components, string(read_Bytes(b, p, int(l), e)))
	}
	if kt.Version != 1 {
		//Name Type is omitted in version 1
		ke.Principal.NameType = read_int32(b, p, e)
	}
	return
}

func read_timestamp(b []byte, p *int, e *binary.ByteOrder) time.Time {
	return time.Unix(int64(read_int32(b, p, e)), 0)
}

func read_int8(b []byte, p *int, e *binary.ByteOrder) (i int8) {
	buf := bytes.NewBuffer(b[*p : *p+1])
	binary.Read(buf, *e, &i)
	*p += 1
	return
}

func read_int16(b []byte, p *int, e *binary.ByteOrder) (i int16) {
	buf := bytes.NewBuffer(b[*p : *p+2])
	binary.Read(buf, *e, &i)
	*p += 2
	return
}

func read_int32(b []byte, p *int, e *binary.ByteOrder) (i int32) {
	buf := bytes.NewBuffer(b[*p : *p+4])
	binary.Read(buf, *e, &i)
	*p += 4
	return
}

func read_Bytes(b []byte, p *int, s int, e *binary.ByteOrder) []byte {
	buf := bytes.NewBuffer(b[*p : *p+s])
	r := make([]byte, s)
	binary.Read(buf, *e, &r)
	*p += s
	return r
}

func IsNativeEndianLittle() bool {
	var x int = 0x012345678
	var p unsafe.Pointer = unsafe.Pointer(&x)
	var bp *[4]byte = (*[4]byte)(p)

	var endian bool
	if 0x01 == bp[0] {
		endian = false
	} else if (0x78 & 0xff) == (bp[0] & 0xff) {
		endian = true
	} else {
		// Default to big endian
		endian = false
	}
	return endian
}