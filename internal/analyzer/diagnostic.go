package analyzer

// Diagnostic carries a portable explanation of incomplete scan evidence.
// File/section context is attached when exported, rather than absolute paths.
type Diagnostic struct {
	Code         string  `json:"code"`
	Severity     string  `json:"severity"`
	Message      string  `json:"message"`
	RepositoryID *string `json:"repository_id"`
	FileID       *string `json:"file_id"`
	SectionID    *string `json:"section_id"`
	Reference    *string `json:"reference"`
	Line         *int    `json:"line"`
}
