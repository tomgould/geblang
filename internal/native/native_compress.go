package native

import (
	"bytes"
	"compress/gzip"
	"geblang/internal/runtime"
	"io"
)

func registerCompress(r *Registry) {
	r.Register("compress", "gzip", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleBytes(args, "compress.gzip")
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		writer := gzip.NewWriter(&out)
		if _, err := writer.Write(data); err != nil {
			_ = writer.Close()
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: out.Bytes()}, nil
	})
	r.Register("compress", "gunzip", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleBytes(args, "compress.gunzip")
		if err != nil {
			return nil, err
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		out, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: out}, nil
	})
}
