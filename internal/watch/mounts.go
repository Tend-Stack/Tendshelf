package watch

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Mount is one line of the mount table: where it is attached and what kind of file system it is.
type Mount struct {
	Point string
	Type  string
}

// networkTypes are the file systems that cross a network. Change events do not arrive from them (the
// server on the other side does not tell this machine what changed), and walking them keeps a NAS
// disk awake, so a folder on one is checked on a schedule instead of watched.
var networkTypes = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "smbfs": true, "smb": true, "afs": true,
	"ceph": true, "glusterfs": true, "9p": true, "davfs": true, "ncpfs": true,
	"fuse.sshfs": true, "fuse.rclone": true, "fuse.davfs2": true, "fuse.s3fs": true, "fuse.gvfsd-fuse": true,
}

// ReadMounts reads the mount table (Linux: /proc/self/mountinfo). Anywhere else, or when it cannot be
// read, it returns nil and every folder counts as local.
func ReadMounts() []Mount {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	return ParseMountInfo(string(b))
}

// ParseMountInfo reads the text of /proc/self/mountinfo. The mount point is the fifth field and the file
// system type is the first field after the " - " separator; spaces and other odd bytes in a mount point
// are written as octal escapes such as \040.
func ParseMountInfo(text string) []Mount {
	var out []Mount
	for _, line := range strings.Split(text, "\n") {
		head, tail, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields := strings.Fields(head)
		typ := strings.Fields(tail)
		if len(fields) < 5 || len(typ) < 1 {
			continue
		}
		out = append(out, Mount{Point: unescapeOctal(fields[4]), Type: typ[0]})
	}
	return out
}

// unescapeOctal turns \040 style escapes back into bytes.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// IsNetwork reports whether path sits on a network file system: the type of the mount with the longest
// mount point that contains it. path should already have its symbolic links resolved.
func IsNetwork(path string, mounts []Mount) bool {
	best, typ := -1, ""
	for _, m := range mounts {
		if !within(m.Point, path) || len(m.Point) <= best {
			continue
		}
		best, typ = len(m.Point), m.Type
	}
	return networkTypes[typ]
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
