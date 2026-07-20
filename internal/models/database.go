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

type PersonTopicModel struct {
	ID             string
	UserID         string
	PersonID       string
	TopicID        string
	Niceness       float64
	Readiness      float64
	Competence     float64
	Capacity       float64
	Confidence     float64
	EvidenceCount  int
	LastObservedAt *time.Time
	Metadata       json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Belief struct {
	ID                string
	UserID            string
	TopicID           string
	Claim             string
	NormalizedClaim   string
	Stance            string
	Confidence        float64
	EvidenceMemoryIDs []string
	LastUpdatedAt     time.Time
	Metadata          json.RawMessage
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type GraphEdge struct {
	ID                string
	UserID            string
	SourceKind        string
	SourceName        string
	TargetKind        string
	TargetName        string
	Relationship      string
	Confidence        float64
	EvidenceMemoryIDs []string
	Metadata          json.RawMessage
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type InteractionOutcome struct {
	ID               string
	UserID           string
	SessionID        string
	MessageID        string
	PersonID         string
	TopicID          string
	Goal             string
	PredictedOutcome string
	ActualOutcome    string
	SuccessScore     float64
	PredictionError  string
	Metadata         json.RawMessage
	IdempotencyKey   string
	RequestHash      string
	MemoryID         string
	ProcessingStatus string
	FailedStage      string
	ProcessingError  string
	CreatedAt        time.Time
	UpdatedAt        time.Time
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

type MemoryEntity struct {
	ID             string
	MemoryItemID   string
	EntityType     string
	EntityName     string
	NormalizedName string
	Confidence     float64
	Metadata       json.RawMessage
	CreatedAt      time.Time
}
