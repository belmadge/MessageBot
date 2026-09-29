package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/portfolio/whatsapp-support/internal/ai"
	"github.com/portfolio/whatsapp-support/internal/httpapi"
	"github.com/portfolio/whatsapp-support/internal/postgres"
	"github.com/portfolio/whatsapp-support/internal/service"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		log.Printf("api stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	provider, err := ai.NewProvider(ai.Config{
		Name:        os.Getenv("AI_PROVIDER"),
		OpenAIKey:   os.Getenv("OPENAI_API_KEY"),
		OpenAIModel: os.Getenv("OPENAI_MODEL"),
	}, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return fmt.Errorf("configure AI provider: %w", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("configure migrations: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	app := service.New(
		postgres.ContactRepository{DB: db},
		postgres.ConversationRepository{DB: db},
		postgres.MessageRepository{DB: db},
		provider,
	)
	server := &http.Server{Addr: ":" + port, Handler: httpapi.New(app), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("API listening on :%s", port)
	return server.ListenAndServe()
}
