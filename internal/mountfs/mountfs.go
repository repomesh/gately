// Package mountfs 把多个本地目录组合成一个虚拟文件系统：根目录只读，按挂载名称列出各目录，
// 每个挂载可单独设为只读。FTP 与 WebDAV 用户挂载多个目录时使用。
package mountfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Mount 是用户可访问的一个目录。
type Mount struct {
	// Name 是虚拟根目录下显示的目录名
	Name string `json:"name"`
	// Path 是本地目录（绝对路径）
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
}

// Validate 校验并规范化挂载列表：名称非空、不含路径分隔符且不重复，目录为绝对路径。
func Validate(mounts []Mount) error {
	seen := map[string]bool{}
	for i := range mounts {
		mt := &mounts[i]
		mt.Name = strings.TrimSpace(mt.Name)
		mt.Path = strings.TrimSpace(mt.Path)
		if mt.Name == "" {
			return errors.New("挂载名称不能为空")
		}
		if mt.Name == "." || mt.Name == ".." || strings.ContainsAny(mt.Name, `/\`) {
			return fmt.Errorf("挂载名称 %q 无效", mt.Name)
		}
		if seen[mt.Name] {
			return fmt.Errorf("挂载名称 %s 重复", mt.Name)
		}
		seen[mt.Name] = true
		if !filepath.IsAbs(mt.Path) {
			return fmt.Errorf("挂载 %s 的目录需为绝对路径", mt.Name)
		}
	}
	return nil
}

// split 把虚拟路径拆分为挂载名称与挂载内路径。根目录返回空名称。
func split(name string) (mount, rest string) {
	p := path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))
	if p == "/" {
		return "", "/"
	}
	p = p[1:]
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i:]
	}
	return p, "/"
}

// errOp 生成带路径的错误，便于 FTP / WebDAV 返回正确的状态码。
func errOp(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// namedInfo 用挂载名称替换挂载目录自身的文件名。
type namedInfo struct {
	fs.FileInfo
	name string
}

func (n namedInfo) Name() string { return n.name }

// mountInfo 返回虚拟根目录下挂载项的信息，目录不存在时仍列出，访问时再报错。
func mountInfo(mt Mount) fs.FileInfo {
	if st, err := os.Stat(mt.Path); err == nil && st.IsDir() {
		return namedInfo{st, mt.Name}
	}
	return dirInfo{name: mt.Name, mod: time.Time{}}
}

// dirInfo 是虚拟目录的信息。
type dirInfo struct {
	name string
	mod  time.Time
}

func (d dirInfo) Name() string       { return d.name }
func (d dirInfo) Size() int64        { return 0 }
func (d dirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (d dirInfo) ModTime() time.Time { return d.mod }
func (d dirInfo) IsDir() bool        { return true }
func (d dirInfo) Sys() any           { return nil }

// rootDir 是虚拟根目录的只读句柄，同时满足 afero.File 与 webdav.File。
type rootDir struct {
	infos []fs.FileInfo
	// pos 为 Readdir 分页读取的位置
	pos int
}

func newRoot(mounts []Mount) *rootDir {
	r := &rootDir{}
	for _, mt := range mounts {
		r.infos = append(r.infos, mountInfo(mt))
	}
	return r
}

func (r *rootDir) Close() error                      { return nil }
func (r *rootDir) Read([]byte) (int, error)          { return 0, errOp("read", "/", fs.ErrInvalid) }
func (r *rootDir) ReadAt([]byte, int64) (int, error) { return 0, errOp("read", "/", fs.ErrInvalid) }
func (r *rootDir) Write([]byte) (int, error)         { return 0, errOp("write", "/", fs.ErrPermission) }
func (r *rootDir) WriteAt([]byte, int64) (int, error) {
	return 0, errOp("write", "/", fs.ErrPermission)
}
func (r *rootDir) WriteString(string) (int, error) { return 0, errOp("write", "/", fs.ErrPermission) }
func (r *rootDir) Truncate(int64) error            { return errOp("truncate", "/", fs.ErrPermission) }
func (r *rootDir) Sync() error                     { return nil }
func (r *rootDir) Name() string                    { return "/" }
func (r *rootDir) Stat() (fs.FileInfo, error)      { return dirInfo{name: "/", mod: time.Now()}, nil }
func (r *rootDir) Seek(offset int64, whence int) (int64, error) {
	// 目录只支持回到开头，用于重新列出
	if offset == 0 && whence == io.SeekStart {
		r.pos = 0
		return 0, nil
	}
	return 0, errOp("seek", "/", fs.ErrInvalid)
}

// Readdir 按 os.File.Readdir 的约定返回：count <= 0 时返回全部，否则分页并在末尾返回 io.EOF。
func (r *rootDir) Readdir(count int) ([]fs.FileInfo, error) {
	rest := r.infos[r.pos:]
	if count <= 0 {
		r.pos = len(r.infos)
		return append([]fs.FileInfo(nil), rest...), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if count > len(rest) {
		count = len(rest)
	}
	r.pos += count
	return append([]fs.FileInfo(nil), rest[:count]...), nil
}

func (r *rootDir) Readdirnames(n int) ([]string, error) {
	infos, err := r.Readdir(n)
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names, err
}

// isWrite 判断打开方式是否包含写入。
func isWrite(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
}
