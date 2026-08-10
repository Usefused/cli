package pagination

// Config is the transport-only pagination v2 contract shared by workspace
// config and Registry API projections. Registry and Engine own semantic
// validation so the CLI cannot drift into a competing policy implementation.
type Config struct {
	Version    int         `yaml:"version" json:"version"`
	Type       string      `yaml:"type" json:"type"`
	Cursor     *Cursor     `yaml:"cursor,omitempty" json:"cursor,omitempty"`
	Offset     *Offset     `yaml:"offset,omitempty" json:"offset,omitempty"`
	PageNumber *PageNumber `yaml:"page_number,omitempty" json:"page_number,omitempty"`
	NextURL    *NextURL    `yaml:"next_url,omitempty" json:"next_url,omitempty"`
	ItemsPath  string      `yaml:"items_path" json:"items_path"`
	Limits     Limits      `yaml:"limits" json:"limits"`
}

type Cursor struct {
	Request RequestTarget `yaml:"request" json:"request"`
	Initial *Scalar       `yaml:"initial,omitempty" json:"initial,omitempty"`
	Next    ValueSource   `yaml:"next" json:"next"`
	HasMore *ValueSource  `yaml:"has_more,omitempty" json:"has_more,omitempty"`
}

type Offset struct {
	Request         RequestTarget `yaml:"request" json:"request"`
	Start           int64         `yaml:"start" json:"start"`
	Increment       Increment     `yaml:"increment" json:"increment"`
	PageSize        *PageSize     `yaml:"page_size,omitempty" json:"page_size,omitempty"`
	NextOffset      *ValueSource  `yaml:"next_offset,omitempty" json:"next_offset,omitempty"`
	TotalItems      *ValueSource  `yaml:"total_items,omitempty" json:"total_items,omitempty"`
	HasMore         *ValueSource  `yaml:"has_more,omitempty" json:"has_more,omitempty"`
	StopOnShortPage bool          `yaml:"stop_on_short_page,omitempty" json:"stop_on_short_page,omitempty"`
}

type PageNumber struct {
	Request         RequestTarget `yaml:"request" json:"request"`
	Start           int64         `yaml:"start" json:"start"`
	Increment       int64         `yaml:"increment" json:"increment"`
	PageSize        *PageSize     `yaml:"page_size,omitempty" json:"page_size,omitempty"`
	TotalPages      *ValueSource  `yaml:"total_pages,omitempty" json:"total_pages,omitempty"`
	HasMore         *ValueSource  `yaml:"has_more,omitempty" json:"has_more,omitempty"`
	StopOnShortPage bool          `yaml:"stop_on_short_page,omitempty" json:"stop_on_short_page,omitempty"`
}

type NextURL struct {
	Next ValueSource `yaml:"next" json:"next"`
}

type PageSize struct {
	Target RequestTarget `yaml:"target" json:"target"`
	Value  int64         `yaml:"value" json:"value"`
}

type Increment struct {
	Mode  string `yaml:"mode" json:"mode"`
	Value int64  `yaml:"value,omitempty" json:"value,omitempty"`
}

type RequestTarget struct {
	Location string `yaml:"location" json:"location"`
	Name     string `yaml:"name" json:"name"`
}

type ValueSource struct {
	Location  string `yaml:"location" json:"location"`
	Path      string `yaml:"path,omitempty" json:"path,omitempty"`
	Name      string `yaml:"name,omitempty" json:"name,omitempty"`
	Relation  string `yaml:"relation,omitempty" json:"relation,omitempty"`
	ValueType string `yaml:"value_type" json:"value_type"`
}

type Scalar struct {
	Type    string  `yaml:"type" json:"type"`
	String  *string `yaml:"string,omitempty" json:"string,omitempty"`
	Integer *int64  `yaml:"integer,omitempty" json:"integer,omitempty"`
}

type Limits struct {
	MaxPages      int64 `yaml:"max_pages" json:"max_pages"`
	MaxItems      int64 `yaml:"max_items" json:"max_items"`
	MaxBytes      int64 `yaml:"max_bytes" json:"max_bytes"`
	MaxDurationMs int64 `yaml:"max_duration_ms" json:"max_duration_ms"`
}
