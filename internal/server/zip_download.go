package server

import (
	"archive/zip"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
)

func serveTaskDirectory(w http.ResponseWriter, r *http.Request, root *os.Root, name string) error {
	// Walk the task subtree through os.Root, rejecting links and device files.
	entries := []string{}
	e := fs.WalkDir(root.FS(), name, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == name {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fail("unsafe_file", "下载目录中包含符号链接")
		}
		if !d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return fail("unsafe_file", "下载目录中包含非常规文件")
			}
			entries = append(entries, p)
			if len(entries) > 1000 {
				return fail("archive_limit", "目录文件超过导出上限")
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name+".zip"))
	archive := zip.NewWriter(w)
	defer archive.Close()
	for _, p := range entries {
		if r.Context().Err() != nil {
			return nil
		}
		f, e := root.Open(p)
		if e != nil {
			return nil
		}
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return nil
		}
		header, e := zip.FileInfoHeader(info)
		if e != nil {
			f.Close()
			return nil
		}
		header.Name = path.Clean(p)
		header.Method = zip.Store
		out, e := archive.CreateHeader(header)
		if e == nil {
			_, e = io.Copy(out, f)
		}
		f.Close()
		if e != nil {
			return nil
		}
	}
	return nil
}
