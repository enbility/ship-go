package api

type RemoteService struct {
	Name       string               `json:"name"`
	Ski        string               `json:"ski"`
	Identifier string               `json:"identifier"`
	Brand      string               `json:"brand"`
	Type       string               `json:"type"`
	Model      string               `json:"model"`
	Serial     string               `json:"serial"`
	Categories []DeviceCategoryType `json:"categories"`
}
