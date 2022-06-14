package main

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/karrick/godirwalk"
	"github.com/krolaw/zipstream"
	"github.com/valyala/fasthttp"
)

var root string
var server *url.URL
var nextQ chan string

func main() {
	nextQ = make(chan string, 24)
	log.Println(os.Args)

	if len(os.Args) < 2 {
		log.Fatalln("unknown command  ")
	}
	if os.Args[1] == "recv" {
		recv()
	} else if os.Args[1] == "send" {
		send()
	} else {
		log.Fatalln("unknown command")
	}
}

func recv() {
	var err error
	if root, err = filepath.Abs(os.Args[2]); err != nil {
		log.Fatalln(err)
	}

	s := fasthttp.Server{
		Handler:            fastHTTPHandler,
		MaxRequestBodySize: 1024 * 1024 * 1024 * 1024,
		StreamRequestBody:  true,
	}
	if err = s.ListenAndServe(":8997"); err != nil {
		log.Fatalln(err)
	}
}

func fastHTTPHandler(ctx *fasthttp.RequestCtx) {
	base := filepath.Join(root, string(ctx.RequestURI()))
	if err := os.MkdirAll(base, 0755); err != nil {
		log.Printf("faieled mkdirall: %v: %+v", base, err)
		ctx.Error("failed", 500)
		return
	}
	// if string(ctx.Request.Header.ContentType()) != "zip" {
	// 	ctx.Error("not zip", 400)
	// 	return
	// }
	length := uint64(ctx.Request.Header.ContentLength())
	// done := uint64(0)
	// done2 := uint64(0)

	bodyReader := ctx.RequestBodyStream()
	zipReader := zipstream.NewReader(bodyReader)

	for {
		zfile, err := zipReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("failed read next file: %+v: %+v", base, err)
			ctx.Error("failed", 500)
			return
		}
		if !zfile.Mode().IsRegular() {
			continue
		}

		fname := filepath.Join(base, zfile.Name)
		ftmp := fname + ".tmp"
		fdir := filepath.Dir(fname)
		if err := os.MkdirAll(fdir, 0755); err != nil {
			log.Printf("failed create dir: %v: %w", fdir, err)
			ctx.Error("failed", 500)
			return
		}

		tfile, err := os.OpenFile(ftmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			log.Printf("failed open file: %v: %+v", ftmp, err)
			ctx.Error("failed", 500)
			return
		}
		defer tfile.Close()
		_, err = io.Copy(tfile, zipReader)
		if err != nil {
			log.Printf("failed copy to file: %v: %+v", ftmp, err)
			ctx.Error("failed", 500)
			return
		}
		// done2 += uint64(n)
		if err = tfile.Close(); err != nil {
			log.Printf("failed close file: %v: %+v", ftmp, err)
			ctx.Error("failed", 500)
			return
		}
		if err = os.Rename(ftmp, fname); err != nil {
			log.Printf("failed rename file: %v -> %v: %+v", ftmp, fname, err)
			ctx.Error("failed", 500)
			return
		}
		if err = os.Chtimes(fname, zfile.Modified, zfile.Modified); err != nil {
			log.Printf("failed set time to file: %v %v: %+v", fname, zfile.Modified, err)
		}
		// log.Println(zfile.Name, len(red), zfile.CompressedSize64, zfile.Mode(), zfile.Modified, zfile.FileInfo())
		// done += zfile.CompressedSize64
	}

	log.Println(base, length/1024/1024)
	// if done == length -
	ctx.SetStatusCode(201)
}

func send() {
	var err error
	if root, err = filepath.Abs(os.Args[2]); err != nil {
		log.Fatalln(err)
	}

	if server, err = url.Parse(os.Args[3]); err != nil {
		log.Fatalln(err)
	}
	log.Printf("root: %v; server: %v", root, server)
	wg := sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for next := range nextQ {
				// log.Printf("next %v", next)
				if err := sendJob(next); err != nil {
					log.Printf("fail %v: %+v", next, err)
					time.Sleep(time.Second)
				}
			}
		}()
	}

	err = godirwalk.Walk(root, &godirwalk.Options{
		Callback: func(osPathname string, de *godirwalk.Dirent) error {
			if !de.IsRegular() {
				return nil
			}
			nextQ <- osPathname
			return nil
		},
		Unsorted: true, // (optional) set true for faster yet non-deterministic enumeration (see godoc)
	})

	close(nextQ)
	wg.Wait()

	if err != nil {
		log.Fatalln(err)
	}
}

func sendJob(next string) error {
	if filepath.Ext(next) == ".zip" {
		return sendJobZip(next)
	}
	return nil
}

func sendJobZip(next string) error {
	file, err := os.Open(next)
	if err != nil {
		return fmt.Errorf("failed open file %v: %w", next, err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed stat file: %v: %w", next, err)
	}

	req := &fasthttp.Request{}
	req.Header.SetMethod(fasthttp.MethodPost)
	req.SetBodyStream(file, int(stat.Size()))

	uri := *server

	path, err := filepath.Rel(root, filepath.Dir(next))
	if err != nil {
		return fmt.Errorf("failed calc relative path: %v -> %v: %w", root, next, err)
	}
	uri.Path = path
	req.SetRequestURI(uri.String())
	req.Header.SetContentType("application/octet-stream")

	res := &fasthttp.Response{}
	err = fasthttp.Do(req, res)
	if err != nil {
		return fmt.Errorf("failed do request: %v: %w", next, err)
	}
	file.Close()

	if res.StatusCode() == 201 {
		if err := os.Rename(next, next+".del"); err != nil {
			log.Printf("failed mark to del file: %v: %v", next, err)
		}
		log.Printf("done %v", next)
	} else {
		return fmt.Errorf("status not 201: %v: %v", res.StatusCode(), string(res.Body()))
	}

	//
	// log.Printf("request done code: %v", res.StatusCode())
	return nil
}
