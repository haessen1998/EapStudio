package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

//go:embed configs/*.yaml profiles/**/*.yaml
var studioFiles embed.FS

func init() {
	application.RegisterEvent[StudioSnapshot]("studio:snapshot-changed")
	application.RegisterEvent[CopilotStreamEvent]("studio:copilot-stream")
}

// main function serves as the application's entry point. It initializes the application, creates a window,
// and starts a goroutine that emits a time-based event every second. It subsequently runs the application and
// logs any error that might occur.
func main() {
	studio, err := NewStudioService(studioFiles)
	if err != nil {
		log.Fatal(err)
	}
	defer studio.close()
	app := application.New(application.Options{
		Name:        "EapStudio",
		Description: "AI-powered SECS/GEM Equipment Integration Studio",
		Services: []application.Service{
			application.NewService(studio),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background colour of the window.
	// 'URL' is the URL that will be loaded into the webview.
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "EapStudio",
		Width:     1440,
		Height:    900,
		MinWidth:  1100,
		MinHeight: 700,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(8, 12, 20),
		URL:              "/",
	})

	studio.start(context.Background())
	go func() {
		for range studio.updateSignal() {
			app.Event.Emit("studio:snapshot-changed", studio.Snapshot())
		}
	}()
	go func() {
		for value := range studio.copilotEventSignal() {
			app.Event.Emit("studio:copilot-stream", value)
		}
	}()

	// Run the application. This blocks until the application has been exited.
	err = app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
