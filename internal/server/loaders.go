package server

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayJamieson/libsql-rest/internal/db"
	"github.com/grafana/sobek"
)

func (s *Server) loadFileHandlers(mux *http.ServeMux) error {

	return filepath.Walk(s.cfg.HandlerDir, func(path string, info fs.FileInfo, err error) error {
		ext := filepath.Ext(path)

		if info.IsDir() || ext != ".js" {
			return nil
		}

		relPath, relErr := filepath.Rel(s.cfg.HandlerDir, path)

		if relErr != nil {
			return err
		}
		name := strings.TrimSuffix(relPath, filepath.Ext(relPath))
		name = filepath.ToSlash(name)

		parts := strings.Split(name, "/")

		// Split the last part by dots to handle dot-separated structure
		lastPart := parts[len(parts)-1]
		dotParts := strings.Split(lastPart, ".")

		// Replace the last part with dot-separated parts
		allParts := append(parts[:len(parts)-1], dotParts...)

		// Build the expected URL
		urlParts := make([]string, 0, len(allParts))

		for _, part := range allParts {
			if part == "index" {
				// Skip index parts in URL construction
				continue
			} else if after, ok := strings.CutPrefix(part, "$"); ok {
				// Convert $paramName to {paramName}
				paramName := after
				urlParts = append(urlParts, "{"+paramName+"}")
			} else {
				urlParts = append(urlParts, part)
			}
		}

		fmt.Printf("Registering js handler route: %-50s | %s\n", fmt.Sprintf("/%s", strings.Join(urlParts, "/")), path)

		if len(urlParts) == 0 {
			mux.HandleFunc("/{$}", s.wrapHandler(path))
		} else {
			mux.HandleFunc(fmt.Sprintf("/%s", strings.Join(urlParts, "/")), s.wrapHandler(path))
		}
		return nil
	})
}

func (s *Server) loadTables() error {
	query, err := s.db.Query(tableQuery)

	if err != nil {
		return err
	}

	results := make([]map[string]any, 0, db.MaxPageSize)
	defer query.Close()

	for query.Next() {
		row := make(map[string]any)
		errScan := db.MapScan(query, row)

		if errScan != nil {
			log.Printf("%v", errScan)
			return err
		}
		results = append(results, row)
	}

	s.metadata["tables"] = results
	return nil
}

func (s *Server) wrapHandler(path string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO cache vm + script instance instead of making one per request ?
		vm := sobek.New()
		addConsoleLog(vm)
		jsContent, err := os.ReadFile(path)

		if err != nil {
			http.Error(w, "error reading handler", http.StatusInternalServerError)
			return
		}
		_, jsErr := vm.RunString(string(jsContent))

		if jsErr != nil {
			http.Error(w, fmt.Sprintf("javaScript error: %v", jsErr), http.StatusInternalServerError)
			return
		}
		sqlFunc, ok := sobek.AssertFunction(vm.Get("sql"))

		if !ok {
			http.Error(w, "no query function", http.StatusInternalServerError)
			return
		}

		sql, err := sqlFunc(sobek.Undefined())

		if err != nil {
			http.Error(w, fmt.Sprintf("sql function error: %v", err), http.StatusInternalServerError)
			return
		}

		w.Write([]byte(sql.String()))
	}
}

func addConsoleLog(runtime *sobek.Runtime) {
	console := runtime.NewObject()

	console.Set("log", func(args ...interface{}) {
		fmt.Println(args...)
	})

	console.Set("error", func(args ...interface{}) {
		allArgs := append([]interface{}{"ERROR:"}, args...)
		fmt.Println(allArgs...)
	})

	console.Set("warn", func(args ...interface{}) {
		allArgs := append([]interface{}{"WARN:"}, args...)
		fmt.Println(allArgs...)
	})

	console.Set("info", func(args ...interface{}) {
		allArgs := append([]interface{}{"INFO:"}, args...)
		fmt.Println(allArgs...)
	})

	runtime.Set("console", console)
}
