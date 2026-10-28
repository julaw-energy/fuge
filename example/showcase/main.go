// Command showcase runs a small live fuge server: a few documented RPC
// methods and a channel behind a real Centrifuge node, with the interactive
// reference mounted so you can click through it and use "Try it".
//
//	go run ./example/showcase   # then open http://localhost:8080/asyncdocs/ (-port to change)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/centrifugal/centrifuge"

	"github.com/julaw-energy/fuge"
)

type Task struct {
	ID    int    `json:"id" required:"true"`
	Title string `json:"title" required:"true"`
	Done  bool   `json:"done" required:"true"`
}

type CreateTaskRequest struct {
	Title string `json:"title" required:"true" doc:"What needs doing"`
}

func (r CreateTaskRequest) Validate() error {
	if len(r.Title) < 3 {
		return fmt.Errorf("title must be at least 3 characters")
	}
	return nil
}

type GetTaskRequest struct {
	ID int `json:"id" required:"true" doc:"Task id"`
}

type ListTasksResponse struct {
	Tasks []Task `json:"tasks" required:"true"`
}

type Empty struct{}

type TaskEvent struct {
	Kind string `json:"kind" required:"true" doc:"created | completed"`
	Task Task   `json:"task" required:"true"`
}

func main() {
	port := flag.String("port", "8080", "port to listen on")
	flag.Parse()

	cfg := fuge.DefaultConfig("Task Board RPC", "1.0.0")
	cfg.Info.Description = "A tiny task board over WebSockets. Call methods, subscribe to the event stream."
	cfg.Servers = map[string]fuge.Server{
		"local": {Host: "localhost:" + *port, Protocol: "ws", Pathname: "/connection/websocket"},
	}
	cfg.SecuritySchemes["bearer"] = fuge.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
	api := fuge.New(cfg)

	node, err := centrifuge.New(centrifuge.Config{})
	if err != nil {
		log.Fatal(err)
	}
	node.OnConnecting(func(context.Context, centrifuge.ConnectEvent) (centrifuge.ConnectReply, error) {
		return centrifuge.ConnectReply{Credentials: &centrifuge.Credentials{UserID: "demo"}}, nil
	})
	node.OnConnect(func(c *centrifuge.Client) { api.Attach(node, c) })

	events := fuge.RegisterChannel[TaskEvent](api, fuge.Channel{
		ID: "task-events", Pattern: "tasks:main", Summary: "Live task events",
		Tags: []string{"Tasks"},
	})

	tasks := []Task{{1, "Write the README", true}, {2, "Ship v0.1.0", false}}
	g := api.Group("task", "Tasks")

	fuge.Register(g, fuge.Operation{
		OperationID: "list-tasks", Method: "list", Summary: "List all tasks",
	}, func(context.Context, *Empty) (*ListTasksResponse, error) {
		return &ListTasksResponse{Tasks: tasks}, nil
	})
	fuge.Register(g, fuge.Operation{
		OperationID: "get-task", Method: "get", Summary: "Get one task by id",
		Errors: []uint32{centrifuge.ErrorNotAvailable.Code},
	}, func(_ context.Context, r *GetTaskRequest) (*Task, error) {
		for _, t := range tasks {
			if t.ID == r.ID {
				return &t, nil
			}
		}
		return nil, fuge.ErrNotFound
	})
	fuge.Register(g, fuge.Operation{
		OperationID: "create-task", Method: "create", Summary: "Create a task",
		Description: "Validates the title, stores the task and publishes a `created` event on `tasks:main`.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, func(_ context.Context, r *CreateTaskRequest) (*Task, error) {
		t := Task{ID: len(tasks) + 1, Title: r.Title}
		tasks = append(tasks, t)
		_ = events.Publish(node, nil, TaskEvent{Kind: "created", Task: t})
		return &t, nil
	})

	if err := node.Run(); err != nil {
		log.Fatal(err)
	}

	// A heartbeat so the channel has something to show: complete a task every few seconds.
	go func() {
		for n := 1; ; n++ {
			time.Sleep(2 * time.Second)
			t := Task{ID: 100 + n, Title: fmt.Sprintf("Background job #%d", n), Done: true}
			_ = events.Publish(node, nil, TaskEvent{Kind: "completed", Task: t})
		}
	}()
	mux := http.NewServeMux()
	mux.Handle("/connection/websocket", centrifuge.NewWebsocketHandler(node, centrifuge.WebsocketConfig{
		CheckOrigin: func(*http.Request) bool { return true },
	}))
	api.ServeDocs(mux)

	srv := &http.Server{Addr: ":" + *port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("docs at http://localhost:%s/asyncdocs/", *port)
	log.Fatal(srv.ListenAndServe())
}
