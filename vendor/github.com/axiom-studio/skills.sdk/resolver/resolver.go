// Package resolver provides template resolution for Axiom skills.
package resolver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Config holds the data needed for template resolution
type Config struct {
	Trigger    map[string]interface{}
	Bindings   map[string]interface{}
	Prev       map[string]interface{}
	Nodes      map[string]interface{}
	Variables  map[string]interface{}
	Self       map[string]interface{}
	Run        map[string]interface{}
	ElapsedMs  int64
}

// Resolver implements executor.TemplateResolver with full template support
type Resolver struct {
	config Config
}

// New creates a new resolver with the given config
func New(config Config) *Resolver {
	return &Resolver{config: config}
}

// ResolveString resolves {{path.to.value}} templates in a string
func (r *Resolver) ResolveString(template string) string {
	result := template
	for {
		start := findTemplateStart(result)
		if start == -1 {
			break
		}
		end := findTemplateEnd(result, start)
		if end == -1 {
			break
		}

		path := strings.TrimSpace(result[start+2 : end])
		value := r.resolvePath(path)

		replacement := ""
		if value != nil {
			switch v := value.(type) {
			case string:
				replacement = v
			case int, int64, int32, float64, float32, bool:
				replacement = fmt.Sprintf("%v", v)
			default:
				if jsonBytes, err := json.Marshal(value); err == nil {
					replacement = string(jsonBytes)
				} else {
					replacement = fmt.Sprintf("%v", value)
				}
			}
		}

		result = result[:start] + replacement + result[end+2:]
	}
	return result
}

// ResolveMap resolves all string values in a map
func (r *Resolver) ResolveMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	result := make(map[string]interface{})
	for k, v := range input {
		switch val := v.(type) {
		case string:
			result[k] = r.ResolveString(val)
		case map[string]interface{}:
			result[k] = r.ResolveMap(val)
		default:
			result[k] = val
		}
	}
	return result
}

// EvaluateCondition evaluates a condition string
func (r *Resolver) EvaluateCondition(condition string) bool {
	resolved := r.ResolveString(condition)

	if strings.Contains(resolved, "==") {
		parts := strings.SplitN(resolved, "==", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0]) == strings.TrimSpace(parts[1])
		}
	}

	if strings.Contains(resolved, "!=") {
		parts := strings.SplitN(resolved, "!=", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0]) != strings.TrimSpace(parts[1])
		}
	}

	return resolved != "" && resolved != "false" && resolved != "0"
}

// SetVariable sets a variable
func (r *Resolver) SetVariable(name string, value interface{}) {
	if r.config.Variables == nil {
		r.config.Variables = make(map[string]interface{})
	}
	r.config.Variables[name] = value
}

// GetStepOutput returns the output of a previous step
func (r *Resolver) GetStepOutput(stepName string) interface{} {
	if r.config.Nodes == nil {
		return nil
	}
	return r.config.Nodes[stepName]
}

// SetStepOutput sets the output of a step
func (r *Resolver) SetStepOutput(stepName string, output interface{}) {
	if r.config.Nodes == nil {
		r.config.Nodes = make(map[string]interface{})
	}
	r.config.Nodes[stepName] = output
}

// GetBinding returns a binding value by name
func (r *Resolver) GetBinding(name string) interface{} {
	if r.config.Bindings == nil {
		return nil
	}
	return r.config.Bindings[name]
}

// GetBindings returns all bindings
func (r *Resolver) GetBindings() map[string]interface{} {
	return r.config.Bindings
}

// GetContextData returns all context data for code execution
func (r *Resolver) GetContextData() map[string]interface{} {
	return map[string]interface{}{
		"bindings":   r.config.Bindings,
		"trigger":    r.config.Trigger,
		"prev":       r.config.Prev,
		"nodes":      r.config.Nodes,
		"vars":       r.config.Variables,
		"run":        r.config.Run,
		"self":       r.config.Self,
		"elapsed_ms": r.config.ElapsedMs,
	}
}

func (r *Resolver) resolvePath(path string) interface{} {
	parts := splitPath(path)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "trigger":
		return getNestedValue(r.config.Trigger, parts[1:])
	case "bindings":
		return getNestedValue(r.config.Bindings, parts[1:])
	case "prev":
		return getNestedValue(r.config.Prev, parts[1:])
	case "self":
		return getNestedValue(r.config.Self, parts[1:])
	case "elapsed_ms":
		return r.config.ElapsedMs
	case "nodes":
		if len(parts) >= 2 {
			nodeOutput := r.config.Nodes[parts[1]]
			return getNestedValue(nodeOutput, parts[2:])
		}
		return r.config.Nodes
	case "var":
		if len(parts) >= 2 {
			return getNestedValue(r.config.Variables[parts[1]], parts[2:])
		}
		return r.config.Variables
	case "run":
		return getNestedValue(r.config.Run, parts[1:])
	}
	return nil
}

func findTemplateStart(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '{' && s[i+1] == '{' {
			return i
		}
	}
	return -1
}

func findTemplateEnd(s string, start int) int {
	for i := start + 2; i < len(s)-1; i++ {
		if s[i] == '}' && s[i+1] == '}' {
			return i
		}
	}
	return -1
}

func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, c := range path {
		if c == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func getNestedValue(data interface{}, path []string) interface{} {
	if len(path) == 0 || data == nil {
		return data
	}

	m, ok := data.(map[string]interface{})
	if !ok {
		return nil
	}

	value, ok := m[path[0]]
	if !ok {
		return nil
	}

	return getNestedValue(value, path[1:])
}

// ============================================================================
// Type-Safe Config Resolution
// ============================================================================

// Binding marks a string field as a binding reference.
type Binding string

// Expr marks a string field as an expression that should always be resolved.
type Expr string

// ResolveConfig resolves a config map into a typed struct.
func ResolveConfig(src map[string]interface{}, dst interface{}, r *Resolver) error {
	dstVal := reflect.ValueOf(dst)
	if dstVal.Kind() != reflect.Ptr || dstVal.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("dst must be a pointer to struct")
	}

	dstVal = dstVal.Elem()
	dstType := dstVal.Type()

	for i := 0; i < dstType.NumField(); i++ {
		field := dstType.Field(i)
		fieldVal := dstVal.Field(i)

		key := field.Tag.Get("json")
		if key == "" || key == "-" {
			key = field.Name
		}
		if idx := strings.Index(key, ","); idx != -1 {
			key = key[:idx]
		}

		srcVal, exists := src[key]
		if !exists {
			def := field.Tag.Get("default")
			if def != "" {
				if err := setFieldValue(fieldVal, def, field.Type, r); err != nil {
					return fmt.Errorf("field %s: %w", key, err)
				}
			}
			continue
		}

		if err := setFieldValue(fieldVal, srcVal, field.Type, r); err != nil {
			return fmt.Errorf("field %s: %w", key, err)
		}
	}

	return nil
}

func setFieldValue(field reflect.Value, src interface{}, fieldType reflect.Type, r *Resolver) error {
	switch fieldType.Kind() {
	case reflect.String:
		s, err := toString(src)
		if err != nil {
			return err
		}
		if strings.Contains(s, "{{") || fieldType.Name() == "Binding" || fieldType.Name() == "Expr" {
			s = r.ResolveString(s)
		}
		field.SetString(s)

	case reflect.Int, reflect.Int64, reflect.Int32:
		i, err := toInt(src)
		if err != nil {
			return err
		}
		field.SetInt(i)

	case reflect.Float64, reflect.Float32:
		f, err := toFloat(src)
		if err != nil {
			return err
		}
		field.SetFloat(f)

	case reflect.Bool:
		b, err := toBool(src)
		if err != nil {
			return err
		}
		field.SetBool(b)

	case reflect.Map:
		if src == nil {
			return nil
		}
		m, ok := src.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map, got %T", src)
		}
		field.Set(reflect.ValueOf(r.ResolveMap(m)))

	case reflect.Slice:
		if src == nil {
			return nil
		}
		slice, ok := src.([]interface{})
		if !ok {
			return fmt.Errorf("expected slice, got %T", src)
		}
		field.Set(reflect.ValueOf(slice))

	case reflect.Interface:
		field.Set(reflect.ValueOf(src))

	default:
		if src != nil {
			srcVal := reflect.ValueOf(src)
			if srcVal.Type().ConvertibleTo(fieldType) {
				field.Set(srcVal.Convert(fieldType))
			}
		}
	}

	return nil
}

func toString(src interface{}) (string, error) {
	if src == nil {
		return "", nil
	}
	switch v := src.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return fmt.Sprintf("%v", src), nil
	}
}

func toInt(src interface{}) (int64, error) {
	if src == nil {
		return 0, nil
	}
	switch v := src.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		var i int64
		_, err := fmt.Sscanf(v, "%d", &i)
		return i, err
	default:
		return 0, fmt.Errorf("cannot convert %T to int", src)
	}
}

func toFloat(src interface{}) (float64, error) {
	if src == nil {
		return 0, nil
	}
	switch v := src.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case string:
		var f float64
		_, err := fmt.Sscanf(v, "%f", &f)
		return f, err
	default:
		return 0, fmt.Errorf("cannot convert %T to float", src)
	}
}

func toBool(src interface{}) (bool, error) {
	if src == nil {
		return false, nil
	}
	switch v := src.(type) {
	case bool:
		return v, nil
	case string:
		lower := strings.ToLower(strings.TrimSpace(v))
		return lower == "true" || lower == "1" || lower == "yes", nil
	default:
		return false, fmt.Errorf("cannot convert %T to bool", src)
	}
}

// ============================================================================
// Schema Generation - Full UI Support
// ============================================================================

// FieldType defines the UI widget type
type FieldType string

const (
	FieldTypeText       FieldType = "text"
	FieldTypeTextarea   FieldType = "textarea"
	FieldTypeNumber     FieldType = "number"
	FieldTypeSelect     FieldType = "select"
	FieldTypeMultiselect FieldType = "multiselect"
	FieldTypeToggle     FieldType = "toggle"
	FieldTypeSlider     FieldType = "slider"
	FieldTypeKeyValue   FieldType = "keyvalue"
	FieldTypeTags       FieldType = "tags"
	FieldTypeCode       FieldType = "code"
	FieldTypeJSON       FieldType = "json"
	FieldTypeCron       FieldType = "cron"
	FieldTypeExpression FieldType = "expression"
)

// SelectOption defines an option for select/multiselect fields
type SelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Icon  string `json:"icon,omitempty"`
}

// Validation defines field validation rules
type Validation struct {
	Pattern string `json:"pattern,omitempty"`
	Min     *int   `json:"min,omitempty"`
	Max     *int   `json:"max,omitempty"`
	Message string `json:"message,omitempty"`
}

// ShowIf defines conditional visibility
type ShowIf struct {
	Field  string      `json:"field"`
	Equals interface{} `json:"equals,omitempty"`
	OneOf  []interface{} `json:"oneOf,omitempty"`
}

// FieldSchema describes a single field in the config schema
type FieldSchema struct {
	Key          string         `json:"key"`
	Label        string         `json:"label"`
	Type         FieldType      `json:"type"`
	Placeholder  string         `json:"placeholder,omitempty"`
	Hint         string         `json:"hint,omitempty"`
	Required     bool           `json:"required,omitempty"`
	Default      interface{}    `json:"default,omitempty"`
	DefaultValue interface{}    `json:"defaultValue,omitempty"`
	Description  string         `json:"description,omitempty"`

	// Type-specific options
	Options    []SelectOption `json:"options,omitempty"`
	Rows       int            `json:"rows,omitempty"`
	Min        *float64       `json:"min,omitempty"`
	Max        *float64       `json:"max,omitempty"`
	Step       float64        `json:"step,omitempty"`
	Suffix     string         `json:"suffix,omitempty"`
	Prefix     string         `json:"prefix,omitempty"`
	Language   string         `json:"language,omitempty"`
	Height     int            `json:"height,omitempty"`
	Multiline  bool           `json:"multiline,omitempty"`
	ShowValue  bool           `json:"showValue,omitempty"`

	// Key-Value specific
	KeyPlaceholder   string `json:"keyPlaceholder,omitempty"`
	ValuePlaceholder string `json:"valuePlaceholder,omitempty"`

	// Validation & Visibility
	Validation *Validation `json:"validation,omitempty"`
	ShowIf     *ShowIf     `json:"showIf,omitempty"`
	Sensitive  bool        `json:"sensitive,omitempty"`

	// Nested fields (for object type)
	Properties map[string]*FieldSchema `json:"properties,omitempty"`
	Items      *FieldSchema            `json:"items,omitempty"`
}

// ConfigSection groups fields together
type ConfigSection struct {
	Title          string         `json:"title,omitempty"`
	Collapsible    bool           `json:"collapsible,omitempty"`
	DefaultExpanded bool           `json:"defaultExpanded,omitempty"`
	Fields         []*FieldSchema `json:"fields"`
	ShowIf         *ShowIf        `json:"showIf,omitempty"`
}

// NodeSchema describes the full schema for a node type
type NodeSchema struct {
	NodeType    string           `json:"nodeType"`
	Name        string           `json:"name,omitempty"`
	DisplayName string           `json:"displayName,omitempty"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Category    string           `json:"category,omitempty"`
	Icon        string           `json:"icon,omitempty"`
	Sections    []*ConfigSection `json:"sections"`
}

// SchemaBuilder helps build node schemas fluently
type SchemaBuilder struct {
	schema *NodeSchema
}

// NewSchemaBuilder creates a new schema builder
func NewSchemaBuilder(nodeType string) *SchemaBuilder {
	return &SchemaBuilder{
		schema: &NodeSchema{
			NodeType: nodeType,
			Sections: []*ConfigSection{},
		},
	}
}

// WithName sets the node name
func (b *SchemaBuilder) WithName(name string) *SchemaBuilder {
	b.schema.Name = name
	b.schema.DisplayName = name
	return b
}

// WithDescription sets the description
func (b *SchemaBuilder) WithDescription(desc string) *SchemaBuilder {
	b.schema.Description = desc
	return b
}

// WithCategory sets the category
func (b *SchemaBuilder) WithCategory(cat string) *SchemaBuilder {
	b.schema.Category = cat
	return b
}

// WithIcon sets the icon
func (b *SchemaBuilder) WithIcon(icon string) *SchemaBuilder {
	b.schema.Icon = icon
	return b
}

// AddSection adds a new section
func (b *SchemaBuilder) AddSection(title string) *SectionBuilder {
	section := &ConfigSection{
		Title:  title,
		Fields: []*FieldSchema{},
	}
	b.schema.Sections = append(b.schema.Sections, section)
	return &SectionBuilder{section: section, builder: b}
}

// Build returns the completed schema
func (b *SchemaBuilder) Build() *NodeSchema {
	return b.schema
}

// SectionBuilder helps build sections
type SectionBuilder struct {
	section *ConfigSection
	builder *SchemaBuilder
}

// Collapsible makes the section collapsible
func (sb *SectionBuilder) Collapsible(defaultExpanded bool) *SectionBuilder {
	sb.section.Collapsible = true
	sb.section.DefaultExpanded = defaultExpanded
	return sb
}

// AddTextField adds a text field
func (sb *SectionBuilder) AddTextField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeText,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddTextareaField adds a textarea field
func (sb *SectionBuilder) AddTextareaField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeTextarea,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddNumberField adds a number field
func (sb *SectionBuilder) AddNumberField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeNumber,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddSelectField adds a select field
func (sb *SectionBuilder) AddSelectField(key, label string, options []SelectOption, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:     key,
		Label:   label,
		Type:    FieldTypeSelect,
		Options: options,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddToggleField adds a toggle field
func (sb *SectionBuilder) AddToggleField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeToggle,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddExpressionField adds an expression field (supports {{}} templates)
func (sb *SectionBuilder) AddExpressionField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeExpression,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddCodeField adds a code editor field
func (sb *SectionBuilder) AddCodeField(key, label, language string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:      key,
		Label:    label,
		Type:     FieldTypeCode,
		Language: language,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddJSONField adds a JSON editor field
func (sb *SectionBuilder) AddJSONField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeJSON,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddKeyValueField adds a key-value field
func (sb *SectionBuilder) AddKeyValueField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeKeyValue,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddTagsField adds a tags field
func (sb *SectionBuilder) AddTagsField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeTags,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddSliderField adds a slider field
func (sb *SectionBuilder) AddSliderField(key, label string, min, max float64, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:       key,
		Label:     label,
		Type:      FieldTypeSlider,
		Min:       &min,
		Max:       &max,
		ShowValue: true,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// AddCronField adds a cron expression field
func (sb *SectionBuilder) AddCronField(key, label string, opts ...FieldOption) *SectionBuilder {
	field := &FieldSchema{
		Key:   key,
		Label: label,
		Type:  FieldTypeCron,
	}
	for _, opt := range opts {
		opt(field)
	}
	sb.section.Fields = append(sb.section.Fields, field)
	return sb
}

// EndSection returns to the schema builder
func (sb *SectionBuilder) EndSection() *SchemaBuilder {
	return sb.builder
}

// FieldOption is a function that modifies a field
type FieldOption func(*FieldSchema)

// WithRequired marks a field as required
func WithRequired() FieldOption {
	return func(f *FieldSchema) {
		f.Required = true
	}
}

// WithDefault sets the default value
func WithDefault(val interface{}) FieldOption {
	return func(f *FieldSchema) {
		f.Default = val
		f.DefaultValue = val
		f.Required = false
	}
}

// WithPlaceholder sets the placeholder text
func WithPlaceholder(text string) FieldOption {
	return func(f *FieldSchema) {
		f.Placeholder = text
	}
}

// WithHint sets the hint text
func WithHint(text string) FieldOption {
	return func(f *FieldSchema) {
		f.Hint = text
	}
}

// WithDescription sets the description
func WithDescription(text string) FieldOption {
	return func(f *FieldSchema) {
		f.Description = text
	}
}

// WithSensitive marks a field as sensitive (password field)
func WithSensitive() FieldOption {
	return func(f *FieldSchema) {
		f.Sensitive = true
	}
}

// WithValidation adds validation rules
func WithValidation(pattern, message string) FieldOption {
	return func(f *FieldSchema) {
		f.Validation = &Validation{
			Pattern: pattern,
			Message: message,
		}
	}
}

// WithMinMax sets min/max validation
func WithMinMax(min, max float64) FieldOption {
	return func(f *FieldSchema) {
		if f.Min == nil {
			f.Min = &min
		}
		if f.Max == nil {
			f.Max = &max
		}
	}
}

// WithShowIf adds conditional visibility
func WithShowIf(field string, equals interface{}) FieldOption {
	return func(f *FieldSchema) {
		f.ShowIf = &ShowIf{
			Field:  field,
			Equals: equals,
		}
	}
}

// WithShowIfOneOf adds conditional visibility with multiple values
func WithShowIfOneOf(field string, values ...interface{}) FieldOption {
	return func(f *FieldSchema) {
		f.ShowIf = &ShowIf{
			Field: field,
			OneOf: values,
		}
	}
}

// WithRows sets the number of rows for textarea
func WithRows(rows int) FieldOption {
	return func(f *FieldSchema) {
		f.Rows = rows
	}
}

// WithHeight sets the height for code/json editors
func WithHeight(height int) FieldOption {
	return func(f *FieldSchema) {
		f.Height = height
	}
}

// WithSuffix sets a suffix for number fields
func WithSuffix(suffix string) FieldOption {
	return func(f *FieldSchema) {
		f.Suffix = suffix
	}
}

// WithPrefix sets a prefix for text fields
func WithPrefix(prefix string) FieldOption {
	return func(f *FieldSchema) {
		f.Prefix = prefix
	}
}

// WithMultiline makes an expression field multiline
func WithMultiline() FieldOption {
	return func(f *FieldSchema) {
		f.Multiline = true
	}
}

// WithStep sets the step for number/slider fields
func WithStep(step float64) FieldOption {
	return func(f *FieldSchema) {
		f.Step = step
	}
}

// WithKeyValuePlaceholders sets placeholders for key-value fields
func WithKeyValuePlaceholders(keyPlaceholder, valuePlaceholder string) FieldOption {
	return func(f *FieldSchema) {
		f.KeyPlaceholder = keyPlaceholder
		f.ValuePlaceholder = valuePlaceholder
	}
}

// ToJSON returns the schema as JSON
func (s *NodeSchema) ToJSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// ============================================================================
// Auto-generation from struct tags
// ============================================================================

// GenerateSchema generates a NodeSchema from a config struct.
// For more control, use SchemaBuilder.
func GenerateSchema(nodeType string, configType interface{}) *NodeSchema {
	t := reflect.TypeOf(configType)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	schema := &NodeSchema{
		NodeType: nodeType,
		Sections: []*ConfigSection{
			{
				Title:  "Configuration",
				Fields: []*FieldSchema{},
			},
		},
	}

	section := schema.Sections[0]

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		key := field.Tag.Get("json")
		if key == "" || key == "-" {
			continue
		}
		if idx := strings.Index(key, ","); idx != -1 {
			key = key[:idx]
		}

		fieldSchema := &FieldSchema{
			Key:   key,
			Label: keyToLabel(key),
		}

		// Determine field type
		fieldSchema.Type = goTypeToFieldType(field.Type)

		// Parse tags
		if desc := field.Tag.Get("description"); desc != "" {
			fieldSchema.Description = desc
			fieldSchema.Hint = desc
		}
		if placeholder := field.Tag.Get("placeholder"); placeholder != "" {
			fieldSchema.Placeholder = placeholder
		}
		if def := field.Tag.Get("default"); def != "" {
			fieldSchema.Default = parseDefault(def, field.Type)
			fieldSchema.DefaultValue = fieldSchema.Default
		} else {
			fieldSchema.Required = true
		}
		if options := field.Tag.Get("options"); options != "" {
			fieldSchema.Options = parseOptions(options)
		}
		if sensitive := field.Tag.Get("sensitive"); sensitive == "true" {
			fieldSchema.Sensitive = true
		}
		if rows := field.Tag.Get("rows"); rows != "" {
			fmt.Sscanf(rows, "%d", &fieldSchema.Rows)
		}
		if language := field.Tag.Get("language"); language != "" {
			fieldSchema.Language = language
		}
		if showIf := field.Tag.Get("showIf"); showIf != "" {
			parts := strings.SplitN(showIf, "=", 2)
			if len(parts) == 2 {
				fieldSchema.ShowIf = &ShowIf{
					Field:  parts[0],
					Equals: parts[1],
				}
			}
		}

		section.Fields = append(section.Fields, fieldSchema)
	}

	return schema
}

func goTypeToFieldType(t reflect.Type) FieldType {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Name() {
	case "Binding":
		return FieldTypeExpression
	case "Expr":
		return FieldTypeExpression
	}

	switch t.Kind() {
	case reflect.String:
		return FieldTypeText
	case reflect.Int, reflect.Int64, reflect.Int32:
		return FieldTypeNumber
	case reflect.Float64, reflect.Float32:
		return FieldTypeNumber
	case reflect.Bool:
		return FieldTypeToggle
	case reflect.Slice:
		return FieldTypeTags
	case reflect.Map:
		return FieldTypeKeyValue
	default:
		return FieldTypeText
	}
}

func keyToLabel(key string) string {
	// Convert camelCase/snake_case to Title Case
	result := ""
	for i, c := range key {
		if i > 0 && (c >= 'A' && c <= 'Z') {
			result += " "
		}
		if c == '_' {
			result += " "
			continue
		}
		result += string(c)
	}
	return strings.Title(result)
}

func parseDefault(def string, t reflect.Type) interface{} {
	switch t.Kind() {
	case reflect.String:
		return def
	case reflect.Int, reflect.Int64, reflect.Int32:
		var i int64
		fmt.Sscanf(def, "%d", &i)
		return i
	case reflect.Float64, reflect.Float32:
		var f float64
		fmt.Sscanf(def, "%f", &f)
		return f
	case reflect.Bool:
		return strings.ToLower(def) == "true"
	default:
		return def
	}
}

func parseOptions(options string) []SelectOption {
	var result []SelectOption
	for _, opt := range strings.Split(options, ",") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		// Check for label:value format
		if parts := strings.SplitN(opt, ":", 2); len(parts) == 2 {
			result = append(result, SelectOption{
				Label: strings.TrimSpace(parts[0]),
				Value: strings.TrimSpace(parts[1]),
			})
		} else {
			result = append(result, SelectOption{
				Label: opt,
				Value: opt,
			})
		}
	}
	return result
}

// ============================================================================
// TypedConfig - Alternative map-based access
// ============================================================================

type TypedConfig struct {
	config   map[string]interface{}
	resolver *Resolver
}

func NewTypedConfig(config map[string]interface{}, resolver *Resolver) *TypedConfig {
	return &TypedConfig{config: config, resolver: resolver}
}

func (c *TypedConfig) Get(name string) interface{} {
	if c.config == nil {
		return nil
	}
	return c.config[name]
}

func (c *TypedConfig) String(name string) string {
	if c.config == nil {
		return ""
	}
	v := c.config[name]
	if v == nil {
		return ""
	}
	s, _ := toString(v)
	if strings.Contains(s, "{{") {
		s = c.resolver.ResolveString(s)
	}
	return s
}

func (c *TypedConfig) StringOr(name, def string) string {
	s := c.String(name)
	if s == "" {
		return def
	}
	return s
}

func (c *TypedConfig) Int(name string) (int, error) {
	if c.config == nil {
		return 0, fmt.Errorf("config is nil")
	}
	i, err := toInt(c.config[name])
	return int(i), err
}

func (c *TypedConfig) IntOr(name string, def int) int {
	i, err := c.Int(name)
	if err != nil {
		return def
	}
	return i
}

func (c *TypedConfig) Bool(name string) (bool, error) {
	if c.config == nil {
		return false, fmt.Errorf("config is nil")
	}
	return toBool(c.config[name])
}

func (c *TypedConfig) BoolOr(name string, def bool) bool {
	b, err := c.Bool(name)
	if err != nil {
		return def
	}
	return b
}

func (c *TypedConfig) Map(name string) (map[string]interface{}, error) {
	if c.config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	v := c.config[name]
	if v == nil {
		return nil, fmt.Errorf("field %s is nil", name)
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("field %s is not a map", name)
	}
	return c.resolver.ResolveMap(m), nil
}

func (c *TypedConfig) Slice(name string) ([]interface{}, error) {
	if c.config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	v := c.config[name]
	if v == nil {
		return nil, fmt.Errorf("field %s is nil", name)
	}
	s, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("field %s is not a slice", name)
	}
	return s, nil
}

func (c *TypedConfig) Binding(name string) interface{} {
	return c.resolver.GetBinding(name)
}

func (c *TypedConfig) BindingString(name string) string {
	v := c.resolver.GetBinding(name)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func (c *TypedConfig) Raw() map[string]interface{} {
	return c.config
}