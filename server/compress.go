package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"sync"

	"tailscale.com/tsweb/compserve"
)

// gzipEncoding adds live compression to compserve.Gzip, which can only
// decompress.
var gzipEncoding = compserve.Encoding{
	Token:      compserve.Gzip.Token,
	Ext:        compserve.Gzip.Ext,
	Decompress: compserve.Gzip.Decompress,
	Compress:   compressGzip,
}

var gzipWriters sync.Pool

func compressGzip(w io.Writer) (io.WriteCloser, error) {
	if gw, ok := gzipWriters.Get().(*gzip.Writer); ok {
		gw.Reset(w)
		return pooledGzipWriter{gw}, nil
	}
	gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	return pooledGzipWriter{gw}, nil
}

type pooledGzipWriter struct{ gw *gzip.Writer }

func (p pooledGzipWriter) Write(b []byte) (int, error) { return p.gw.Write(b) }

// Flush has no return value so compserve's writer can flush it.
func (p pooledGzipWriter) Flush() { _ = p.gw.Flush() }

func (p pooledGzipWriter) Close() error {
	err := p.gw.Close()
	gzipWriters.Put(p.gw)
	return err
}

// compressMiddleware compresses responses with zstd or gzip, whichever the
// client accepts, preferring zstd.
func compressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := compserve.CompressWriter(w, r, compserve.Zstd, gzipEncoding)
		defer cw.Close()
		next.ServeHTTP(cw, r)
	})
}
