// Command soapmacui startet die Oberfläche.
package main

import (
	"embed"
	"log"

	"github.com/apeters/soapmacui/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	a, err := app.New()
	if err != nil {
		log.Fatalf("Start fehlgeschlagen: %v", err)
	}

	err = wails.Run(&options.App{
		Title:     "SoapMacUi",
		Width:     1440,
		Height:    900,
		MinWidth:  1040,
		MinHeight: 640,

		// Das Fenster ist durchscheinend, damit die Vibrancy-Schicht von macOS
		// unter der Oberfläche sichtbar wird — echter Systemblur statt CSS-Fake.
		BackgroundColour: &options.RGBA{R: 6, G: 8, B: 17, A: 1},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        a.Startup,
		Bind:             []any{a},

		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "SoapMacUi",
				Message: "SOAP-Schnittstellen testen.\nNativ auf Apple Silicon.",
			},
		},
	})
	if err != nil {
		log.Fatalf("Laufzeitfehler: %v", err)
	}
}
