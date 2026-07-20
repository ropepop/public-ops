package storage

import (
	"fmt"
	"syscall"
)

type Usage interface {
	UsedBytes() (int64, error)
}

type Filesystem struct {
	Path string
}

func (f Filesystem) UsedBytes() (int64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(f.Path, &stats); err != nil {
		return 0, fmt.Errorf("inspect download filesystem %q: %w", f.Path, err)
	}
	blocks := uint64(stats.Blocks)
	free := uint64(stats.Bfree)
	if free > blocks {
		return 0, fmt.Errorf("inspect download filesystem %q: invalid block counts", f.Path)
	}
	blockSize := uint64(stats.Bsize)
	used := blocks - free
	if blockSize != 0 && used > uint64(^uint64(0)>>1)/blockSize {
		return 0, fmt.Errorf("inspect download filesystem %q: usage overflow", f.Path)
	}
	return int64(used * blockSize), nil
}
