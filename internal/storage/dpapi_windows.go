package storage

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

type DPAPI struct{}

func (DPAPI) Protect(b []byte) ([]byte, error) {
	var out windows.DataBlob
	in := windows.DataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		in.Data = &b[0]
	}
	if e := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); e != nil {
		return nil, e
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func (DPAPI) Unprotect(b []byte) ([]byte, error) {
	var out windows.DataBlob
	in := windows.DataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		in.Data = &b[0]
	}
	if e := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); e != nil {
		return nil, e
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func replaceFile(from, to string) error {
	f, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	t, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
