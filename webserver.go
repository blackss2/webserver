package webserver

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/session"
)

// WebServer struct
type WebServer struct {
	// delimiters
	left  string
	right string
	// views folder
	directory string
	// views extension
	extension string
	// layout variable name that incapsulates the template
	layout string
	// determines if the engine parsed all templates
	loaded bool
	// reload on each render
	reload bool
	// debug prints the parsed templates
	debug bool
	// has watch directory
	watch bool
	// lock for funcmap and templates
	mutex sync.RWMutex
	// template funcmap
	funcmap map[string]interface{}
	// templates
	Templates *template.Template
	// app
	app *fiber.App
	// session store
	store *session.Store
	// embedFiles
	embedFiles *embed.FS
}

// New returns a HTML render engine for Fiber
func NewWebServer(directory string, embedFiles *embed.FS, cfg *session.Config) *WebServer {
	web := &WebServer{
		left:       "<%",
		right:      "%>",
		directory:  directory,
		extension:  ".html",
		layout:     "embed",
		funcmap:    make(map[string]interface{}),
		embedFiles: embedFiles,
	}
	if cfg != nil {
		web.store = session.New(*cfg)
	}

	web.AddFunc(web.layout, func() error {
		return fmt.Errorf("layout called unexpectedly")
	})
	web.AddFunc("marshal", func(v interface{}) string {
		a, _ := json.Marshal(v)
		return string(a)
	})

	if fi, err := os.Stat(web.directory); err == nil && fi.IsDir() {
		WebPath, err := filepath.Abs(web.directory)
		if err != nil {
			log.Fatalln(err)
		}
		NewFileWatcher(WebPath, func(ev string, path string) {
			if strings.HasPrefix(filepath.Ext(path), ".htm") {
				web.reload = true
			}
		})
		web.watch = true
	}

	web.app = fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			if err != nil {
				code := http.StatusInternalServerError
				if he, ok := err.(*fiber.Error); ok {
					code = he.Code
				}
				log.Println(err, c.Request().URI().String())
				c.Status(code).SendString(err.Error())
			}
			return nil
		},
		Views: web,
	})
	return web
}

// App returns fiber App
func (web *WebServer) Route(basePath string, fn func(fiber.Router, *session.Store)) {
	if basePath == "/" {
		fn(web.app, web.store)
	} else {
		fn(web.app.Group(basePath), web.store)
	}
}

// Layout defines the variable name that will incapsulate the template
func (web *WebServer) Layout(key string) *WebServer {
	web.layout = key
	return web
}

// AddFunc adds the function to the template's function map.
// It is legal to overwrite elements of the default actions
func (web *WebServer) AddFunc(name string, fn interface{}) *WebServer {
	web.mutex.Lock()
	web.funcmap[name] = fn
	web.mutex.Unlock()
	return web
}

// Debug will print the parsed templates when Load is triggered.
func (web *WebServer) Debug(enabled bool) *WebServer {
	if !web.debug && enabled {
		web.app.Use(recover.New(recover.Config{
			EnableStackTrace: true,
		}))
	}
	web.debug = enabled
	return web
}

// Load parses the templates to the engine.
func (web *WebServer) Load() error {
	if web.loaded {
		return nil
	}
	// race safe
	web.mutex.Lock()
	defer web.mutex.Unlock()
	web.Templates = template.New(web.directory)

	// Set template settings
	web.Templates.Delims(web.left, web.right)
	web.Templates.Funcs(web.funcmap)

	// notify engine that we parsed all templates
	web.loaded = true

	handler := func(path string) error {
		if filepath.Ext(path) == web.extension {
			rel, err := filepath.Rel(web.directory, path)
			if err != nil {
				return err
			}
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			name = strings.TrimSuffix(name, web.extension)
			if _, err := web.Templates.New(name).Parse(string(data)); err != nil {
				return err
			}
			if web.debug {
				fmt.Printf("views: parsed template: %s\n", name)
			}
		}
		return nil
	}

	if web.watch {
		if err := filepath.Walk(web.directory, func(path string, fi os.FileInfo, err error) error {
			return handler(path)
		}); err != nil {
			return err
		}
	} else {
		if web.embedFiles != nil {
			if err := fs.WalkDir(web.embedFiles, ".", func(path string, d fs.DirEntry, err error) error {
				return handler(path)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Render will execute the template name along with the given values.
func (web *WebServer) Render(out io.Writer, template string, binding interface{}, layout ...string) error {
	if !web.loaded || web.reload {
		if web.reload {
			web.loaded = false
		}
		if err := web.Load(); err != nil {
			return err
		}
		web.reload = false
	}

	tmpl := web.Templates.Lookup(template)
	if tmpl == nil {
		return fmt.Errorf("render: template %s does not exist", template)
	}
	if len(layout) > 0 && layout[0] != "" {
		lay := web.Templates.Lookup(layout[0])
		if lay == nil {
			return fmt.Errorf("render: layout %s does not exist", layout[0])
		}
		web.mutex.Lock()
		defer web.mutex.Unlock()
		lay.Funcs(map[string]interface{}{
			web.layout: func() error {
				return tmpl.Execute(out, binding)
			},
		})
		return lay.Execute(out, binding)
	}
	return tmpl.Execute(out, binding)
}

func (web *WebServer) Static(prefix string, folder string, m ...fiber.Handler) {
	var executableModTime = time.Now()
	if path, err := os.Executable(); err == nil {
		if fi, err := os.Stat(path); err == nil {
			executableModTime = fi.ModTime()
		}
	}

	embedPathMap := map[string]bool{}
	fs.WalkDir(web.embedFiles, ".", func(path string, d fs.DirEntry, err error) error {
		embedPathMap[filepath.ToSlash(path)] = true
		return nil
	})

	root := path.Join(web.directory, folder)
	h := func(c *fiber.Ctx) error {
		fname := c.Params("*")
		fpath := path.Join(root, fname)
		if web.watch {
			absPath, err := filepath.Abs(fpath)
			if err != nil {
				return err
			}
			return c.SendFile(absPath)
		} else if web.embedFiles != nil {
			if !embedPathMap[fpath] {
				return c.SendStatus(http.StatusNotFound)
			} else if data, err := web.embedFiles.ReadFile(fpath); err != nil {
				return c.SendStatus(http.StatusNotFound)
			} else {
				if t, err := time.Parse(http.TimeFormat, c.Get(fiber.HeaderIfModifiedSince)); err == nil && executableModTime.Before(t.Add(1*time.Second)) {
					return c.SendStatus(http.StatusNotModified)
				}
				h := md5.Sum(data)
				fileHash := hex.EncodeToString(h[:])
				if hash := c.Get(fiber.HeaderIfNoneMatch); hash == fileHash {
					return c.SendStatus(http.StatusNotModified)
				}
				c.Set(fiber.HeaderETag, fileHash)
				c.Set(fiber.HeaderLastModified, executableModTime.UTC().Format(http.TimeFormat))

				ctype := mime.TypeByExtension(filepath.Ext(fname))
				if ctype == "" {
					ctype = http.DetectContentType(data)
				}
				if len(ctype) > 0 {
					c.Set(fiber.HeaderContentType, ctype)
				}
				c.Status(http.StatusOK)

				if len(data) > 0 {
					if c.Get(fiber.HeaderContentEncoding) == "" && strings.Contains(c.Get(fiber.HeaderAcceptEncoding), "gzip") {
						var buffer bytes.Buffer
						writer := gzip.NewWriter(&buffer)
						if _, err := writer.Write(data); err != nil {
							return err
						}
						writer.Close()

						if _, err := io.Copy(c.Response().BodyWriter(), &buffer); err != nil {
							return err
						}
						c.Set(fiber.HeaderContentLength, strconv.Itoa(buffer.Len()))
						c.Set(fiber.HeaderContentEncoding, "gzip")
					} else {
						if _, err := c.Response().BodyWriter().Write(data); err != nil {
							return err
						}
						c.Set(fiber.HeaderContentLength, strconv.Itoa(len(data)))
					}
				} else {
					c.Set(fiber.HeaderContentLength, "0")
				}
				return nil
			}
		} else {
			return c.SendStatus(http.StatusNotFound)
		}
	}
	if prefix == "/" {
		web.app.Get("/*", append([]fiber.Handler{func(c *fiber.Ctx) error {
			fname := c.Params("*")
			if len(fname) > 0 {
				if err := h(c); err != nil {
					return err
				}
				status := c.Response().StatusCode()
				if status != http.StatusNotFound && status != http.StatusForbidden {
					return nil
				}
				// Reset response to default
				c.Set(fiber.HeaderContentType, "")
				c.Response().SetStatusCode(http.StatusOK)
				c.Response().SetBodyString("")
			}
			// Next middleware
			return c.Next()
		}}, m...)...)
	} else {
		web.app.Get(prefix+"/*", append([]fiber.Handler{h}, m...)...)
	}
}

// Listener can be used to pass a custom listener.
func (web *WebServer) Listener(ln net.Listener) error {
	return web.app.Listener(ln)
}

// Listen serves HTTP requests from the given addr.
//
//  app.Listen(":8080")
//  app.Listen("127.0.0.1:8080")
func (web *WebServer) Listen(addr string) error {
	return web.app.Listen(addr)
}

// ListenTLS serves HTTPs requests from the given addr.
// certFile and keyFile are the paths to TLS certificate and key file.

//  app.ListenTLS(":8080", "./cert.pem", "./cert.key")
//  app.ListenTLS(":8080", "./cert.pem", "./cert.key")
func (web *WebServer) ListenTLS(addr, certFile, keyFile string) error {
	return web.app.ListenTLS(addr, certFile, keyFile)
}
