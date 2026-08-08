package client

type Organization struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Type          string `json:"type"`
	Overages      bool   `json:"overages"`
	RequireMFA    bool   `json:"require_mfa"`
	BlockedReads  bool   `json:"blocked_reads"`
	BlockedWrites bool   `json:"blocked_writes"`
	PlanID        string `json:"plan_id"`
	PlanTimeline  string `json:"plan_timeline"`
	Platform      string `json:"platform"`
}

type Group struct {
	Name             string   `json:"name"`
	UUID             string   `json:"uuid"`
	Locations        []string `json:"locations"`
	Primary          string   `json:"primary"`
	DeleteProtection bool     `json:"delete_protection"`
}

type GroupListResponse struct {
	Groups []Group `json:"groups"`
}

type GroupResponse struct {
	Group Group `json:"group"`
}

type GroupConfiguration struct {
	DeleteProtection bool `json:"delete_protection"`
}

type Database struct {
	Name             string   `json:"Name"`
	UUID             string   `json:"DbId"`
	Hostname         string   `json:"Hostname"`
	Group            string   `json:"group"`
	Regions          []string `json:"regions"`
	PrimaryRegion    string   `json:"primaryRegion"`
	DeleteProtection bool     `json:"delete_protection"`
	BlockedReads     bool     `json:"block_reads"`
	BlockedWrites    bool     `json:"block_writes"`
}

type DatabaseListResponse struct {
	Databases []Database `json:"databases"`
}

type DatabaseResponse struct {
	Database Database `json:"database"`
}

type DatabaseConfiguration struct {
	SizeLimit        string `json:"size_limit"`
	DeleteProtection bool   `json:"delete_protection"`
	BlockedReads     bool   `json:"block_reads"`
	BlockedWrites    bool   `json:"block_writes"`
}

type UpdateDatabaseConfigurationRequest struct {
	SizeLimit        string `json:"size_limit,omitempty"`
	DeleteProtection bool   `json:"delete_protection"`
}

type LocationListResponse struct {
	Locations map[string]string `json:"locations"`
}

type CreateGroupRequest struct {
	Name     string `json:"name"`
	Location string `json:"location"`
}

type CreateDatabaseRequest struct {
	Name      string `json:"name"`
	Group     string `json:"group"`
	SizeLimit string `json:"size_limit,omitempty"`
}
