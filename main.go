package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/karrick/godirwalk"
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
	}
	if err = s.ListenAndServe(":8997"); err != nil {
		log.Fatalln(err)
	}
}

func fastHTTPHandler(ctx *fasthttp.RequestCtx) {
	base := filepath.Join(root, string(ctx.RequestURI()))
	if err := os.MkdirAll(base, 755); err != nil {
		log.Printf("faieled mkdirall: %v: %+v", base, err)
		ctx.Error("failed", 500)
		return
	}
	log.Println(base)

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
				log.Printf("next %v", next)
				if err := sendJob(next); err != nil {
					log.Printf("fail %v: %+v", next, err)
					time.Sleep(time.Second)
				} else {
					log.Printf("done %v", next)
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
	req := &fasthttp.Request{}
	req.SetBodyStream(file, -1)

	uri := *server
	path, err := filepath.Rel(root, strings.TrimSuffix(next, ".zip"))
	if err != nil {
		return fmt.Errorf("failed calc relative path: %v -> %v: %w", root, next, err)
	}
	uri.Path = path
	req.SetRequestURI(uri.String())
	req.Header.SetContentType("zip")

	res := &fasthttp.Response{}
	err = fasthttp.Do(req, res)
	if err != nil {
		return fmt.Errorf("failed do request: %v: %w", next, err)
	}

	log.Printf("request done code: %v", res.StatusCode())
	return nil
}
