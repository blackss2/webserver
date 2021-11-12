# webserver

webserver that supports these features based on fiber web server and html engine.
1. fiber web server with html engine template
2. embed webfile directory(load data template and public file both if it is not nil)
3. watch and reload changed file when webfile directory exist in same root
1) check webfile directory in root -> automatically use and watch this directory
2) use embed files if it is exist

# example
all html and public is under webfiles in exmaple

ex)
- ./webfiles/main.html
- ./webfiles/public/css/style.css
- ./webfiles/public/images/text.images
```
package main

import (
	"embed"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/session"
	"github.com/gofiber/storage/sqlite3"
	"github.com/gofiber/websocket/v2"
)

//go:embed webfiles
var staticFiles embed.FS

func main() {
	web := NewWebServer("./webfiles", &staticFiles, &session.Config{
		Storage: sqlite3.New(sqlite3.Config{
			Database:   "./sessions.db",
			Table:      "sessions",
			Reset:      false,
			GCInterval: 10 * time.Second,
		}),
	})
	web.Static("/", "/public")
	web.Debug(true)
	web.watch = false //TEMP

	web.Route("/", RouteMain)
	web.Route("/ws", RouteWebsocket)

	web.Listen(":3000")
}

func RouteMain(grp fiber.Router, store *session.Store) {
	grp.Get("/", func(c *fiber.Ctx) error {
		// Get session from storage
		sess, err := store.Get(c)
		if err != nil {
			return err
		}

		// Get value
		name := sess.Get("name")

		// Set key/value
		sess.Set("name", "john")

		// Save session
		if err := sess.Save(); err != nil {
			return err
		}
		return c.Render("main", fiber.Map{
			"name": name,
		})
	})
}

func RouteWebsocket(grp fiber.Router, store *session.Store) {
	grp.Use("/", func(c *fiber.Ctx) error {
		// IsWebSocketUpgrade returns true if the client
		// requested upgrade to the WebSocket protocol.
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	grp.Get("/:id", websocket.New(func(c *websocket.Conn) {
		// c.Locals is added to the *websocket.Conn
		log.Println(c.Locals("allowed"))  // true
		log.Println(c.Params("id"))       // 123
		log.Println(c.Query("v"))         // 1.0
		log.Println(c.Cookies("session")) // ""

		// websocket.Conn bindings https://pkg.go.dev/github.com/fasthttp/websocket?tab=doc#pkg-index
		var (
			mt  int
			msg []byte
			err error
		)
		for {
			if mt, msg, err = c.ReadMessage(); err != nil {
				log.Println("read:", err)
				break
			}
			log.Printf("recv: %s", msg)

			if err = c.WriteMessage(mt, msg); err != nil {
				log.Println("write:", err)
				break
			}
		}
	}))
}
```
