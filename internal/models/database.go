package models

import (
	"encoding/json"
	"time"
)

type User struct {
	ID          string
	ExternalID  string
	Email       string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Session struct {
	ID         string
	UserID     string
	ExternalID string
	Title      string
	Metadata   json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Message struct {
	ID        string
	SessionID string
	UserID    string
	Role      string
	Content   string
	Model     string
	RequestID string
	Metadata  json.RawMessage
	CreatedAt time.Time
}

type Person struct {
	ID             string
	UserID         string
	Name           string
	NormalizedName string
	Aliases        []string
	Metadata       json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Topic struct {
	ID             string
	UserID         string
	Name           string
	NormalizedName string
	Aliases        []string
	Metadata       json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type MemoryItem struct {
	ID              string
	UserID          string
	SessionID       string
	SourceMessageID string
	QdrantPointID   string
	MemoryType      string
	Source          string
	RawText         string
	Summary         string
	People          []string
	Topics          []string
	Importance      float64
	Utility         float64
	BeliefImpact    float64
	Confidence      float64
	ExpiresAt       *time.Time
	Metadata        json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
