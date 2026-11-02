package salesforce

import "time"

// Token is the bootstrap result from the sf CLI.
type Token struct {
	AccessToken string
	InstanceURL string
	APIVersion  string
}

type cliResponse[T any] struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Result  T      `json:"result"`
}

type accessTokenResult struct {
	AccessToken string `json:"accessToken"`
}

type orgDisplayResult struct {
	InstanceURL string `json:"instanceUrl"`
	APIVersion  string `json:"apiVersion"`
}

type describeResponse struct {
	Name   string     `json:"name"`
	Label  string     `json:"label"`
	Fields []fieldDTO `json:"fields"`
}

type fieldDTO struct {
	Name                string   `json:"name"`
	Label               string   `json:"label"`
	Type                string   `json:"type"`
	Length              int      `json:"length"`
	Precision           int      `json:"precision"`
	Scale               int      `json:"scale"`
	Nillable            bool     `json:"nillable"`
	Unique              bool     `json:"unique"`
	ReferenceTo         []string `json:"referenceTo"`
	RelationshipName    string   `json:"relationshipName"`
	Calculated          bool     `json:"calculated"`
	CompoundFieldName   string   `json:"compoundFieldName"`
	AutoNumber          bool     `json:"autoNumber"`
	HTMLFormatted       bool     `json:"htmlFormatted"`
	DeprecatedAndHidden bool     `json:"deprecatedAndHidden"`
}

type queryResponse struct {
	TotalSize      int              `json:"totalSize"`
	Done           bool             `json:"done"`
	NextRecordsURL string           `json:"nextRecordsUrl"`
	Records        []map[string]any `json:"records"`
}

type jobCreateRequest struct {
	Operation       string `json:"operation"`
	Query           string `json:"query"`
	ContentType     string `json:"contentType"`
	ColumnDelimiter string `json:"columnDelimiter"`
	LineEnding      string `json:"lineEnding"`
}

type jobInfo struct {
	ID                     string `json:"id"`
	State                  string `json:"state"`
	NumberRecordsProcessed int    `json:"numberRecordsProcessed"`
	ErrorMessage           string `json:"errorMessage"`
}

type salesforceError struct {
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
}

const (
	stateJobComplete = "JobComplete"
	stateFailed      = "Failed"
	stateAborted     = "Aborted"
)

var pollInterval = time.Second

type globalDescribeResponse struct {
	SObjects []globalSObject `json:"sobjects"`
}

type globalSObject struct {
	Name                string `json:"name"`
	Custom              bool   `json:"custom"`
	Queryable           bool   `json:"queryable"`
	DeprecatedAndHidden bool   `json:"deprecatedAndHidden"`
}
