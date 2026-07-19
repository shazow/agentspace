package storefs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

func TestSanitizeOpenFlags(t *testing.T) {
	got, errno := sanitizeOpenFlags(unix.O_RDONLY | unix.O_NOATIME | unix.O_CLOEXEC)
	if errno != 0 {
		t.Fatalf("read-only open: %v", errno)
	}
	if got&unix.O_NOATIME != 0 {
		t.Fatalf("O_NOATIME was not removed: %#x", got)
	}
	if got&unix.O_CLOEXEC == 0 {
		t.Fatalf("unrelated flag was removed: %#x", got)
	}
}

func TestSanitizeOpenFlagsRejectsMutation(t *testing.T) {
	for _, flags := range []uint32{
		unix.O_WRONLY,
		unix.O_RDWR,
		unix.O_RDONLY | unix.O_TRUNC,
		unix.O_RDONLY | unix.O_CREAT,
	} {
		if _, errno := sanitizeOpenFlags(flags); errno != unix.EROFS {
			t.Errorf("flags %#x: got %v, want EROFS", flags, errno)
		}
	}
}

func TestNewRootRequiresDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoot(path); err == nil {
		t.Fatal("NewRoot accepted a regular file")
	}
}

func TestRootLooksUpAndReadsFile(t *testing.T) {
	dir := t.TempDir()
	const contents = "immutable store data"
	if err := os.WriteFile(filepath.Join(dir, "item"), []byte(contents), 0o444); err != nil {
		t.Fatal(err)
	}

	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = fs.NewNodeFS(root, &fs.Options{})
	child, errno := root.(fs.NodeLookuper).Lookup(context.Background(), "item", &fuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("lookup: %v", errno)
	}
	root.EmbeddedInode().AddChild("item", child, false)
	node, ok := child.Operations().(*readOnlyNode)
	if !ok {
		t.Fatalf("child operations type %T, want *readOnlyNode", child.Operations())
	}
	fh, _, errno := node.Open(context.Background(), unix.O_RDONLY|unix.O_NOATIME)
	if errno != 0 {
		t.Fatalf("open: %v", errno)
	}
	result, errno := fh.(fs.FileReader).Read(context.Background(), make([]byte, len(contents)), 0)
	if errno != 0 {
		t.Fatalf("read: %v", errno)
	}
	got, status := result.Bytes(make([]byte, len(contents)))
	if status != fuse.OK {
		t.Fatalf("read result: %v", status)
	}
	if string(got) != contents {
		t.Fatalf("got %q, want %q", got, contents)
	}
}

func TestReadOnlyNodePreservesSymlinksAndXattrs(t *testing.T) {
	dir := t.TempDir()
	itemPath := filepath.Join(dir, "item")
	if err := os.WriteFile(itemPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("item", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(itemPath, "user.agentspace", []byte("yes"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(itemPath, 0o444); err != nil {
		t.Fatal(err)
	}

	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = fs.NewNodeFS(root, &fs.Options{})
	stream, errno := root.(fs.NodeReaddirer).Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("readdir: %v", errno)
	}
	defer stream.Close()
	entries := map[string]bool{}
	for stream.HasNext() {
		entry, errno := stream.Next()
		if errno != 0 {
			t.Fatalf("readdir next: %v", errno)
		}
		entries[entry.Name] = true
	}
	if !entries["item"] || !entries["link"] {
		t.Fatalf("directory entries: %v", entries)
	}

	lookup := func(name string) *readOnlyNode {
		child, errno := root.(fs.NodeLookuper).Lookup(context.Background(), name, &fuse.EntryOut{})
		if errno != 0 {
			t.Fatalf("lookup %s: %v", name, errno)
		}
		root.EmbeddedInode().AddChild(name, child, false)
		return child.Operations().(*readOnlyNode)
	}

	link, errno := lookup("link").Readlink(context.Background())
	if errno != 0 || string(link) != "item" {
		t.Fatalf("readlink: got %q, %v", link, errno)
	}
	buf := make([]byte, 16)
	size, errno := lookup("item").Getxattr(context.Background(), "user.agentspace", buf)
	if errno != 0 || string(buf[:size]) != "yes" {
		t.Fatalf("getxattr: got %q, %v", buf[:size], errno)
	}
}

func TestReadOnlyNodeRejectsIoctls(t *testing.T) {
	node := &readOnlyNode{}
	if _, errno := node.Ioctl(context.Background(), nil, unix.FS_IOC_SETFLAGS, 0, make([]byte, 4), nil); errno != syscall.ENOTTY {
		t.Fatalf("got %v, want ENOTTY", errno)
	}
}

func TestReadOnlyNodeRejectsMutations(t *testing.T) {
	ctx := context.Background()
	node := &readOnlyNode{}
	checks := map[string]func() syscall.Errno{
		"setattr": func() syscall.Errno {
			return node.Setattr(ctx, nil, &fuse.SetAttrIn{}, &fuse.AttrOut{})
		},
		"setxattr":    func() syscall.Errno { return node.Setxattr(ctx, "user.test", nil, 0) },
		"removexattr": func() syscall.Errno { return node.Removexattr(ctx, "user.test") },
		"write": func() syscall.Errno {
			_, errno := node.Write(ctx, nil, []byte("x"), 0)
			return errno
		},
		"allocate": func() syscall.Errno { return node.Allocate(ctx, nil, 0, 1, 0) },
		"copy-file-range": func() syscall.Errno {
			_, errno := node.CopyFileRange(ctx, nil, 0, node.EmbeddedInode(), nil, 0, 1, 0)
			return errno
		},
		"mkdir": func() syscall.Errno {
			_, errno := node.Mkdir(ctx, "child", 0o755, &fuse.EntryOut{})
			return errno
		},
		"mknod": func() syscall.Errno {
			_, errno := node.Mknod(ctx, "child", 0o644, 0, &fuse.EntryOut{})
			return errno
		},
		"link": func() syscall.Errno {
			_, errno := node.Link(ctx, node, "child", &fuse.EntryOut{})
			return errno
		},
		"symlink": func() syscall.Errno {
			_, errno := node.Symlink(ctx, "target", "child", &fuse.EntryOut{})
			return errno
		},
		"create": func() syscall.Errno {
			_, _, _, errno := node.Create(ctx, "child", unix.O_WRONLY, 0o644, &fuse.EntryOut{})
			return errno
		},
		"unlink": func() syscall.Errno { return node.Unlink(ctx, "child") },
		"rmdir":  func() syscall.Errno { return node.Rmdir(ctx, "child") },
		"rename": func() syscall.Errno { return node.Rename(ctx, "old", node, "new", 0) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if errno := check(); errno != syscall.EROFS {
				t.Fatalf("got %v, want EROFS", errno)
			}
		})
	}
}
