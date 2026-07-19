package storefs

import (
	"context"
	"fmt"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

type readOnlyNode struct {
	fs.LoopbackNode
}

var (
	_ fs.NodeOpener         = (*readOnlyNode)(nil)
	_ fs.NodeSetattrer      = (*readOnlyNode)(nil)
	_ fs.NodeSetxattrer     = (*readOnlyNode)(nil)
	_ fs.NodeRemovexattrer  = (*readOnlyNode)(nil)
	_ fs.NodeIoctler        = (*readOnlyNode)(nil)
	_ fs.NodeWriter         = (*readOnlyNode)(nil)
	_ fs.NodeAllocater      = (*readOnlyNode)(nil)
	_ fs.NodeCopyFileRanger = (*readOnlyNode)(nil)
	_ fs.NodeMkdirer        = (*readOnlyNode)(nil)
	_ fs.NodeMknoder        = (*readOnlyNode)(nil)
	_ fs.NodeLinker         = (*readOnlyNode)(nil)
	_ fs.NodeSymlinker      = (*readOnlyNode)(nil)
	_ fs.NodeCreater        = (*readOnlyNode)(nil)
	_ fs.NodeUnlinker       = (*readOnlyNode)(nil)
	_ fs.NodeRmdirer        = (*readOnlyNode)(nil)
	_ fs.NodeRenamer        = (*readOnlyNode)(nil)
)

// NewRoot creates a loopback inode tree rooted at path. All child nodes use
// readOnlyNode so the policy is preserved across lookups.
func NewRoot(path string) (fs.InodeEmbedder, error) {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return nil, err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return nil, fmt.Errorf("%s is not a directory", path)
	}

	rootData := &fs.LoopbackRoot{
		Path: path,
		Dev:  uint64(stat.Dev),
	}
	rootData.NewNode = func(data *fs.LoopbackRoot, _ *fs.Inode, _ string, _ *syscall.Stat_t) fs.InodeEmbedder {
		return &readOnlyNode{LoopbackNode: fs.LoopbackNode{RootData: data}}
	}
	root := rootData.NewNode(rootData, nil, "", &stat)
	rootData.RootNode = root
	return root, nil
}

func (n *readOnlyNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	flags, errno := sanitizeOpenFlags(flags)
	if errno != 0 {
		return nil, 0, errno
	}
	return n.LoopbackNode.Open(ctx, flags)
}

func (*readOnlyNode) Setattr(context.Context, fs.FileHandle, *fuse.SetAttrIn, *fuse.AttrOut) syscall.Errno {
	return syscall.EROFS
}

func (*readOnlyNode) Setxattr(context.Context, string, []byte, uint32) syscall.Errno {
	return syscall.EROFS
}

func (*readOnlyNode) Removexattr(context.Context, string) syscall.Errno {
	return syscall.EROFS
}

func (*readOnlyNode) Ioctl(context.Context, fs.FileHandle, uint32, uint64, []byte, []byte) (int32, syscall.Errno) {
	return 0, syscall.ENOTTY
}

func (*readOnlyNode) Write(context.Context, fs.FileHandle, []byte, int64) (uint32, syscall.Errno) {
	return 0, syscall.EROFS
}

func (*readOnlyNode) Allocate(context.Context, fs.FileHandle, uint64, uint64, uint32) syscall.Errno {
	return syscall.EROFS
}

func (*readOnlyNode) CopyFileRange(context.Context, fs.FileHandle, uint64, *fs.Inode, fs.FileHandle, uint64, uint64, uint64) (uint32, syscall.Errno) {
	return 0, syscall.EROFS
}

func (*readOnlyNode) Mkdir(context.Context, string, uint32, *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return nil, syscall.EROFS
}

func (*readOnlyNode) Mknod(context.Context, string, uint32, uint32, *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return nil, syscall.EROFS
}

func (*readOnlyNode) Link(context.Context, fs.InodeEmbedder, string, *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return nil, syscall.EROFS
}

func (*readOnlyNode) Symlink(context.Context, string, string, *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return nil, syscall.EROFS
}

func (*readOnlyNode) Create(context.Context, string, uint32, uint32, *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	return nil, nil, 0, syscall.EROFS
}

func (*readOnlyNode) Unlink(context.Context, string) syscall.Errno {
	return syscall.EROFS
}

func (*readOnlyNode) Rmdir(context.Context, string) syscall.Errno {
	return syscall.EROFS
}

func (*readOnlyNode) Rename(context.Context, string, fs.InodeEmbedder, string, uint32) syscall.Errno {
	return syscall.EROFS
}

func sanitizeOpenFlags(flags uint32) (uint32, syscall.Errno) {
	const mutating = unix.O_APPEND | unix.O_CREAT | unix.O_EXCL | unix.O_TMPFILE | unix.O_TRUNC
	if flags&unix.O_ACCMODE != unix.O_RDONLY || flags&mutating != 0 {
		return 0, syscall.EROFS
	}
	return flags &^ unix.O_NOATIME, 0
}
