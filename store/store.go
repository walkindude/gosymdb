// Package store defines the storage interface for gosymdb.
//
// The Store interface is the single point of abstraction over the underlying
// database. cmd/ and indexer/ program against Store, not against database/sql
// directly. This allows alternative backend implementations
// without touching any query logic.
package store

import "context"

// Store is the full storage interface. Most callers receive a Store.
// Backend implementations satisfy this interface.
type Store interface {
	ReadStore
	WriteStore
	SchemaStore
}

// SchemaStore handles DDL and lifecycle.
type SchemaStore interface {
	// Migrate brings the schema to the latest version. Safe to call on an
	// empty DB (creates all tables) or an existing DB (applies only missing
	// migrations). Replaces the old EnsureSchema + error-string-sniffing
	// approach with proper versioned migrations.
	Migrate(ctx context.Context) error

	// ResetSchema drops all tables and recreates the schema at the latest
	// version. Used for --force rebuilds. All data is lost.
	ResetSchema(ctx context.Context) error

	// SchemaVersion returns the highest migration version that has been
	// applied. Returns 0 if the DB is empty or has no schema_version table.
	SchemaVersion(ctx context.Context) (int, error)

	// Close releases the underlying database connection.
	Close() error
}

// WriteStore covers all data-mutation operations used by the indexer.
type WriteStore interface {
	// IndexBatch begins a transactional write session for one module. The
	// caller must call Commit or Rollback on the returned IndexBatch.
	IndexBatch(ctx context.Context, moduleRoot string) (IndexBatch, error)

	// PurgeModule removes all indexed data for moduleRoot across all tables.
	PurgeModule(ctx context.Context, moduleRoot string) error

	// UpsertModule inserts the module root if not already present.
	UpsertModule(ctx context.Context, moduleRoot string) error

	// UpsertPackageMeta upserts a package_meta row.
	UpsertPackageMeta(ctx context.Context, moduleRoot, packagePath, indexedAt string) error

	// UpsertPackageFile upserts a package_files row (per-file hash).
	UpsertPackageFile(ctx context.Context, moduleRoot, packagePath, filePath, fileHash string) error

	// UpdatePackageFilesHash updates the files_hash column in package_meta.
	UpdatePackageFilesHash(ctx context.Context, moduleRoot, packagePath, filesHash string) error

	// InsertIndexMeta records a completed index run.
	InsertIndexMeta(ctx context.Context, meta IndexMeta) error
}

// IndexBatch is a transactional batch for inserting symbols, calls, and
// related edges for a single module. Implementations use a DB transaction.
type IndexBatch interface {
	InsertSymbol(s Symbol) error
	InsertCall(c Call) error
	InsertUnresolved(u UnresolvedCall) error
	InsertImplements(imp Implements) error
	InsertRef(r Call) error // kind="ref"
	InsertTypeRef(tr TypeRef) error
	Commit() error
	Rollback() error
}

// ReadStore covers all query operations used by cmd/.
type ReadStore interface {
	// Symbol queries
	FindSymbols(ctx context.Context, opts FindOpts) (FindResult, error)
	CountSymbols(ctx context.Context, opts FindOpts) (int, error)
	DefSymbol(ctx context.Context, name, pkg string) ([]SymbolRow, error)

	// Symbol resolution helpers (cmd/hint.go)
	ResolveSymbolName(ctx context.Context, name, pkg string) ([]string, error)
	SymbolHint(ctx context.Context, symbol string) ([]string, error)
	InterfaceDispatchHint(ctx context.Context, symbol string) ([]string, error)

	// Call graph — callers
	DirectCallers(ctx context.Context, targets []string, pkg string, limit int) ([]CallerRow, error)
	CountDirectCallers(ctx context.Context, symbol string) (int, error)
	FuzzyCallTargets(ctx context.Context, symbol string) ([]string, error)
	UnresolvedCallers(ctx context.Context, symbol string, fuzzy bool, limit int) ([]UnresolvedCallerRow, error)

	// Call graph — callees
	DirectCallees(ctx context.Context, opts CalleesOpts) ([]CalleeRow, error)
	CountCallees(ctx context.Context, opts CalleesOpts) (int, error)

	// Blast radius (recursive CTE)
	BlastRadius(ctx context.Context, opts BlastRadiusOpts) ([]BlastRadiusRow, error)

	// Dead code
	DeadSymbols(ctx context.Context, opts DeadOpts) (DeadResult, error)

	// Package inventory
	ListPackages(ctx context.Context) ([]PackageRow, error)

	// Health / diagnostics
	HealthStats(ctx context.Context) (*HealthResult, error)

	// Interface satisfaction
	FindImplementors(ctx context.Context, opts ImplementorsOpts) ([]ImplementorRow, error)

	// Type references
	FindReferences(ctx context.Context, opts ReferencesOpts) (ReferencesResult, error)
	CountReferences(ctx context.Context, opts ReferencesOpts) (int, error)

	// Staleness detection (indexer/stale.go)
	IndexedCommit(ctx context.Context) (string, error)
	PackageFiles(ctx context.Context, moduleRoot, packagePath string) ([]PackageFile, error)
	StoredFilesHash(ctx context.Context, moduleRoot, packagePath string) (string, error)
	AllPackagePaths(ctx context.Context) ([]PackageMetaRow, error)

	// Index command helpers
	IndexedModuleRoots(ctx context.Context) ([]string, error)
	ModuleRootForPackage(ctx context.Context, packagePath string) (string, error)

	// env.go
	HasFileTracking(ctx context.Context) (bool, error)
}

// ---- Data types ----

// Symbol represents an indexed Go symbol.
type Symbol struct {
	ModuleRoot  string
	PackagePath string
	PackageName string
	FilePath    string
	Name        string
	Kind        string // func, method, type, interface, var, const
	Recv        string
	Signature   string
	FQName      string
	Exported    bool
	Line        int
	Col         int
}

// Call represents a resolved call edge or function-reference edge.
type Call struct {
	ModuleRoot  string
	PackagePath string
	FilePath    string
	Line        int
	Col         int
	FromFQName  string
	ToFQName    string
	CalleeExpr  string
	Kind        string // "call" or "ref"
}

// UnresolvedCall represents a call that could not be statically resolved.
type UnresolvedCall struct {
	ModuleRoot  string
	PackagePath string
	FilePath    string
	Line        int
	Col         int
	FromFQName  string
	Expr        string
	Reason      string
}

// Implements records that ImplFQName satisfies IfaceFQName.
type Implements struct {
	ModuleRoot  string
	IfacePkg    string
	IfaceFQName string
	ImplPkg     string
	ImplFQName  string
	IsPointer   bool
}

// TypeRef records a type-level reference (assertion, conversion, embed, field
// access, composite literal).
type TypeRef struct {
	ModuleRoot  string
	PackagePath string
	FilePath    string
	Line        int
	Col         int
	FromFQName  string
	ToFQName    string
	RefKind     string
	Expr        string
}

// IndexMeta is written at the end of a successful index run.
type IndexMeta struct {
	ToolVersion   string
	GoVersion     string
	IndexedAt     string
	Root          string
	Warnings      int
	IndexedCommit string
}

// ---- Option and result types ----

// FindOpts parameterises the FindSymbols query.
type FindOpts struct {
	Query string // substring matched against fqname, name, signature
	Pkg   string // package_path prefix
	Kind  string // exact kind filter
	File  string // file_path substring
	Limit int
}

// FindResult is returned by FindSymbols.
type FindResult struct {
	Symbols      []SymbolRow
	TotalMatched int
}

// SymbolRow is a query result row for symbol lookups.
type SymbolRow struct {
	FQName      string
	Name        string
	Kind        string
	File        string
	Line        int
	Col         int
	Signature   string
	PackagePath string
}

// CalleesOpts parameterises DirectCallees / CountCallees.
type CalleesOpts struct {
	Symbol string
	Fuzzy  bool
	Pkg    string
	Unique bool
	Limit  int
}

// CallerRow is a single result from DirectCallers.
type CallerRow struct {
	From  string
	Name  string // short name
	To    string
	File  string
	Line  int
	Col   int
	Kind  string
	Depth int
}

// CalleeRow is a single result from DirectCallees.
type CalleeRow struct {
	FQName      string
	Name        string
	File        string
	Line        int
	Col         int
	Kind        string
	PackagePath string
}

// UnresolvedCallerRow is a result from UnresolvedCallers.
type UnresolvedCallerRow struct {
	From string
	Expr string
	File string
	Line int
	Col  int
}

// BlastRadiusOpts parameterises BlastRadius.
type BlastRadiusOpts struct {
	Symbol       string
	Depth        int
	Fuzzy        bool
	Pkg          string
	ExcludeTests bool
	Limit        int
}

// BlastRadiusRow is a single result from BlastRadius.
type BlastRadiusRow struct {
	FQName  string
	Package string
	File    string
	Line    int
	Depth   int
}

// DeadOpts parameterises DeadSymbols.
type DeadOpts struct {
	Kind            string
	Pkg             string
	IncludeExported bool
	Limit           int
}

// DeadResult is returned by DeadSymbols.
type DeadResult struct {
	Symbols      []SymbolRow
	TotalMatched int
}

// PackageRow is a single result from ListPackages.
type PackageRow struct {
	Path          string
	SymbolCount   int
	FuncCount     int
	ExportedCount int
}

// HealthResult is returned by HealthStats.
type HealthResult struct {
	ToolVersion     string
	GoVersion       string
	IndexedAt       string
	Root            string
	Warnings        int
	SymbolCount     int
	CallCount       int
	UnresolvedCount int
	TypeRefCount    int
	TopUnresolved   []UnresolvedExpr
}

// UnresolvedExpr is a top-N entry from HealthStats.
type UnresolvedExpr struct {
	Expr  string
	Count int
}

// ImplementorsOpts parameterises FindImplementors.
type ImplementorsOpts struct {
	Iface string
	Type  string
	Limit int
}

// ImplementorRow is a single result from FindImplementors.
type ImplementorRow struct {
	Iface     string
	Impl      string
	IsPointer bool
}

// ReferencesOpts parameterises FindReferences / CountReferences.
type ReferencesOpts struct {
	Symbol  string
	Pkg     string
	RefKind string
	From    string
	Limit   int
}

// ReferencesResult is returned by FindReferences.
type ReferencesResult struct {
	Refs         []RefRow
	TotalMatched int
}

// RefRow is a single result from FindReferences.
type RefRow struct {
	FromFQName  string
	ToFQName    string
	RefKind     string
	File        string
	Line        int
	Col         int
	Expr        string
	PackagePath string
}

// PackageFile is a per-file hash entry used by staleness detection.
type PackageFile struct {
	FilePath string
	FileHash string
}

// PackageMetaRow is returned by AllPackagePaths.
type PackageMetaRow struct {
	ModuleRoot  string
	PackagePath string
}
