package profile

type EquipmentProfile struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name    string `yaml:"name" json:"name"`
	Vendor  string `yaml:"vendor" json:"vendor"`
	Model   string `yaml:"model" json:"model"`
	Version string `yaml:"version" json:"version"`
	Adapter string `yaml:"adapter" json:"adapter"`
}

type Spec struct {
	Variables map[uint64]VariableDefinition `yaml:"variables" json:"variables"`
	Reports   map[uint64]ReportDefinition   `yaml:"reports" json:"reports"`
	Events    map[uint64]EventDefinition    `yaml:"events" json:"events"`
	Commands  map[string]CommandDefinition  `yaml:"commands" json:"commands"`
	Simulator SimulatorDefinition           `yaml:"simulator" json:"simulator"`
}

type VariableDefinition struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Description string `yaml:"description" json:"description"`
}

type ReportDefinition struct {
	Name      string   `yaml:"name" json:"name"`
	Variables []uint64 `yaml:"variables" json:"variables"`
}

type EventDefinition struct {
	Name        string                     `yaml:"name" json:"name"`
	DisplayName string                     `yaml:"displayName" json:"displayName"`
	Reports     []uint64                   `yaml:"reports" json:"reports"`
	Mapping     map[string]FieldDefinition `yaml:"mapping" json:"mapping"`
}

type FieldDefinition struct {
	Report   uint64 `yaml:"report" json:"report"`
	Variable uint64 `yaml:"variable" json:"variable"`
}

type CommandDefinition struct {
	DisplayName  string   `yaml:"displayName" json:"displayName"`
	Stream       uint8    `yaml:"stream" json:"stream"`
	Function     uint8    `yaml:"function" json:"function"`
	Wait         bool     `yaml:"wait" json:"wait"`
	Parameters   []string `yaml:"parameters" json:"parameters"`
	SuccessEvent string   `yaml:"successEvent" json:"successEvent"`
	FailureEvent string   `yaml:"failureEvent" json:"failureEvent"`
	SuccessAck   *uint8   `yaml:"successAck" json:"successAck"`
}

type SimulatorDefinition struct {
	Scenarios map[string]SimulatorScenario `yaml:"scenarios" json:"scenarios"`
}

type SimulatorScenario struct {
	DisplayName string          `yaml:"displayName" json:"displayName"`
	Event       string          `yaml:"event" json:"event"`
	Data        map[string]any  `yaml:"data" json:"data"`
	Direction   string          `yaml:"direction" json:"direction"`
	Message     MessageTemplate `yaml:"message" json:"message"`
}

type MessageTemplate struct {
	Stream   uint8          `yaml:"stream" json:"stream"`
	Function uint8          `yaml:"function" json:"function"`
	Wait     bool           `yaml:"wait" json:"wait"`
	SML      string         `yaml:"sml" json:"sml"`
	Fields   map[string]any `yaml:"fields" json:"fields"`
}

type CompiledProfile struct {
	EquipmentProfile
	VariablePositions map[uint64]map[uint64]int
	EventsByName      map[string]uint64
}
