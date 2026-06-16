package transpilert

import "os"

// Typed adapters for the Geblang io module's file surface. They match the
// interpreter's happy-path return values; on failure they panic an IOError so
// top-level recovery renders the engine's uncaught class. The uncaught text
// carries no source line on this path (a Tier-1 limitation).

func ioFail(err error) *Error {
	return &Error{Class: "IOError", Message: err.Error(), Parents: []string{"Error"}}
}

// ReadText returns the file contents as a string.
func ReadText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(ioFail(err))
	}
	return string(data)
}

// WriteText writes content to path, truncating any existing file.
func WriteText(path, content string) any {
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		panic(ioFail(err))
	}
	return nil
}

// Mkdir creates path and any missing parent directories.
func Mkdir(path string, mode int64) any {
	if err := os.MkdirAll(path, os.FileMode(mode)); err != nil {
		panic(ioFail(err))
	}
	return nil
}

// AppendText appends content to path, creating it if absent.
func AppendText(path, content string) any {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		panic(ioFail(err))
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		panic(ioFail(err))
	}
	return nil
}

// ReadBytes returns the file contents as a byte slice.
func ReadBytes(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(ioFail(err))
	}
	return data
}

// WriteBytes writes data to path, truncating any existing file.
func WriteBytes(path string, data []byte) any {
	if err := os.WriteFile(path, data, 0o666); err != nil {
		panic(ioFail(err))
	}
	return nil
}

// AppendBytes appends data to path, creating it if absent.
func AppendBytes(path string, data []byte) any {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		panic(ioFail(err))
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		panic(ioFail(err))
	}
	return nil
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Remove deletes path and any children, matching io.remove (os.RemoveAll).
func Remove(path string) any {
	if err := os.RemoveAll(path); err != nil {
		panic(ioFail(err))
	}
	return nil
}
