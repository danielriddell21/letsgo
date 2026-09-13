package sbom

// The subset of CycloneDX 1.6 that a Go release needs. Field order here is the
// field order in the output, which is part of what makes it reproducible.

type document struct {
	BOMFormat    string       `json:"bomFormat"`
	SpecVersion  string       `json:"specVersion"`
	SerialNumber string       `json:"serialNumber"`
	Version      int          `json:"version"`
	Metadata     metadata     `json:"metadata"`
	Components   []component  `json:"components,omitempty"`
	Dependencies []dependency `json:"dependencies,omitempty"`
}

type metadata struct {
	Timestamp string    `json:"timestamp"`
	Tools     tools     `json:"tools"`
	Component component `json:"component"`
}

type tools struct {
	Components []component `json:"components"`
}

type component struct {
	Type               string              `json:"type"`
	BOMRef             string              `json:"bom-ref,omitempty"`
	Name               string              `json:"name"`
	Version            string              `json:"version,omitempty"`
	Scope              string              `json:"scope,omitempty"`
	PURL               string              `json:"purl,omitempty"`
	Hashes             []hash              `json:"hashes,omitempty"`
	ExternalReferences []externalReference `json:"externalReferences,omitempty"`
	Properties         []property          `json:"properties,omitempty"`
}

type hash struct {
	Algorithm string `json:"alg"`
	Content   string `json:"content"`
}

type externalReference struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}
