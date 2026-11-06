package ebook

import (
	"crypto/md5" // #nosec G501 -- KOReader names a document by this digest; it is an identifier, never a secret
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// PartialMD5 is the digest KOReader calls util.partialMD5 and uses as a document's name when it syncs
// progress: the MD5 of 1024-byte samples taken at offset 0 and then at 1024<<(2*i) for i = 0..10, stopping
// at the first offset past the end of the file. A last sample may be shorter than 1024 bytes. The result
// is 32 lower-case hex characters. It only reads the file.
func PartialMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New() // #nosec G401 -- see the import note
	buf := make([]byte, 1024)
	offsets := make([]int64, 0, 12)
	offsets = append(offsets, 0)
	for i := 0; i <= 10; i++ {
		offsets = append(offsets, int64(1024)<<(2*i))
	}
	for _, off := range offsets {
		n, err := f.ReadAt(buf, off)
		if n > 0 {
			h.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			if n == 0 {
				break
			}
			continue // a short last sample; the next offset is further past the end and reads nothing
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
