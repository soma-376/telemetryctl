package main

// AppInfo is owned by the GUI process.
type AppInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
