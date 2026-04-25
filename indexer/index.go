package indexer

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"
)

// fileResult holds the hash result for a single file.
type fileResult struct {
	path string
	hash string
	err  error
}

// PurgeModule deletes all indexed data for the given moduleRoot.
// Used when a module is removed from disk and detected as orphaned.
func PurgeModule(db *sql.DB, moduleRoot string) error {
	for _, tbl := range []string{"symbols", "calls", "unresolved_calls", "implements", "package_files", "type_refs", "impl_methods"} {
		if _, err := db.Exec("DELETE FROM "+tbl+" WHERE module_root = ?", moduleRoot); err != nil {
			return err
		}
	}
	if _, err := db.Exec("DELETE FROM package_meta WHERE module_root = ?", moduleRoot); err != nil {
		return err
	}
	if _, err := db.Exec("DELETE FROM modules WHERE root = ?", moduleRoot); err != nil {
		return err
	}
	return nil
}

// indexRun holds shared state for a single IndexModule invocation.
type indexRun struct {
	db         *sql.DB
	tx         *sql.Tx
	moduleRoot string
	pkgs       []*packages.Package

	insertSymbol      *sql.Stmt
	insertCall        *sql.Stmt
	insertUnresolved  *sql.Stmt
	insertImpl        *sql.Stmt
	insertImplMethod  *sql.Stmt
	insertIfaceMethod *sql.Stmt

	symbolCount     int
	callCount       int
	unresolvedCount int
	typeRefCount    int
}

// buildFileByPos builds the file-path lookup map for a single package.
func buildFileByPos(pkg *packages.Package) map[*ast.File]string {
	m := make(map[*ast.File]string, len(pkg.Syntax))
	for i, f := range pkg.Syntax {
		filePath := ""
		if i < len(pkg.CompiledGoFiles) {
			filePath = pkg.CompiledGoFiles[i]
		}
		m[f] = filePath
	}
	return m
}

// recordSymbol inserts a symbol row and increments the counter.
func (r *indexRun) recordSymbol(pkg *packages.Package, filePath string, obj types.Object, kind, recv string) {
	pos := pkg.Fset.PositionFor(obj.Pos(), false)
	if _, err := r.insertSymbol.Exec(
		r.moduleRoot, pkg.PkgPath, pkg.Name, filePath,
		obj.Name(), kind, recv, obj.Type().String(), objectFQName(obj), boolToInt(obj.Exported()), pos.Line, pos.Column,
	); err == nil {
		r.symbolCount++
	}
}

func (r *indexRun) recordSyntheticSymbol(pkg *packages.Package, filePath, name, kind, recv, sig, fqname string, exported bool, pos token.Position) {
	if _, err := r.insertSymbol.Exec(
		r.moduleRoot, pkg.PkgPath, pkg.Name, filePath,
		name, kind, recv, sig, fqname, boolToInt(exported), pos.Line, pos.Column,
	); err == nil {
		r.symbolCount++
	}
}

// indexGenDeclSpecs processes type and value specs within a GenDecl.
func (r *indexRun) indexGenDeclSpecs(pkg *packages.Package, filePath string, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			obj := pkg.TypesInfo.Defs[s.Name]
			if obj == nil {
				continue
			}
			kind := "type"
			if _, ok := obj.Type().Underlying().(*types.Interface); ok {
				kind = "interface"
			}
			r.recordSymbol(pkg, filePath, obj, kind, "")
			if kind == "interface" {
				r.recordInterfaceMethods(pkg, filePath, s)
			}
		case *ast.ValueSpec:
			kind := "var"
			if decl.Tok == token.CONST {
				kind = "const"
			}
			for _, ident := range s.Names {
				obj := pkg.TypesInfo.Defs[ident]
				if obj == nil {
					continue
				}
				r.recordSymbol(pkg, filePath, obj, kind, "")
			}
		}
	}
}

func (r *indexRun) recordInterfaceMethods(pkg *packages.Package, filePath string, spec *ast.TypeSpec) {
	obj := pkg.TypesInfo.Defs[spec.Name]
	if obj == nil {
		return
	}
	iface, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		return
	}
	parent := fmt.Sprintf("%s.%s", pkg.PkgPath, spec.Name.Name)
	for _, field := range interfaceMethodFields(spec) {
		for _, name := range field.Names {
			m := ifaceMethodByName(iface, name.Name)
			if m == nil {
				continue
			}
			pos := pkg.Fset.PositionFor(name.Pos(), false)
			r.recordSyntheticSymbol(
				pkg,
				filePath,
				name.Name,
				"method",
				parent,
				m.Type().String(),
				parent+"."+name.Name,
				m.Exported(),
				pos,
			)
		}
	}
}

func interfaceMethodFields(spec *ast.TypeSpec) []*ast.Field {
	it, ok := spec.Type.(*ast.InterfaceType)
	if !ok || it.Methods == nil {
		return nil
	}
	fields := make([]*ast.Field, 0, len(it.Methods.List))
	for _, field := range it.Methods.List {
		if len(field.Names) == 0 {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func ifaceMethodByName(iface *types.Interface, name string) *types.Func {
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		if m != nil && m.Name() == name {
			return m
		}
	}
	return nil
}

// indexPackageSymbols extracts symbols (funcs, methods, types, vars, consts)
// from all loaded packages and inserts them into the symbols table.
func (r *indexRun) indexPackageSymbols() {
	for _, pkg := range r.pkgs {
		if pkg.Types == nil || pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
			continue
		}
		fileByPos := buildFileByPos(pkg)

		for _, f := range pkg.Syntax {
			filePath := fileByPos[f]
			ast.Inspect(f, func(n ast.Node) bool {
				switch decl := n.(type) {
				case *ast.FuncDecl:
					obj := pkg.TypesInfo.Defs[decl.Name]
					if obj == nil {
						return true
					}
					kind := "func"
					recv := ""
					if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
						kind = "method"
						recv = typeString(sig.Recv().Type())
					}
					r.recordSymbol(pkg, filePath, obj, kind, recv)
				case *ast.GenDecl:
					r.indexGenDeclSpecs(pkg, filePath, decl)
				}
				return true
			})
		}
	}
}

// scanCallsInNode walks an AST subtree for call expressions and records edges.
func scanCallsInNode(pkg *packages.Package, from, filePath string, node ast.Node,
	onCall func(from, to, filePath, expr string, line, col int),
	onUnresolved func(from, filePath, expr string, line, col int),
) {
	if from == "" || node == nil {
		return
	}
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		toObj := resolveCallTarget(pkg.TypesInfo, call.Fun)
		if toObj == nil {
			pos := pkg.Fset.PositionFor(call.Lparen, false)
			onUnresolved(from, filePath, exprString(call.Fun), pos.Line, pos.Column)
			return true
		}
		to := objectFQName(toObj)
		if to == "" {
			return true
		}
		pos := pkg.Fset.PositionFor(call.Lparen, false)
		onCall(from, to, filePath, exprString(call.Fun), pos.Line, pos.Column)
		return true
	})
}

// indexCallGraph extracts call edges and unresolved calls from function bodies
// and package-scope variable initializers.
func (r *indexRun) indexCallGraph() {
	for _, pkg := range r.pkgs {
		if pkg.Types == nil || pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
			continue
		}
		r.indexPackageCallGraph(pkg)
	}
}

func (r *indexRun) indexPackageCallGraph(pkg *packages.Package) {
	fileByPos := buildFileByPos(pkg)
	onCall := r.makeCallEdgeRecorder(pkg)
	onUnresolved := r.makeUnresolvedEdgeRecorder(pkg)
	for _, f := range pkg.Syntax {
		filePath := fileByPos[f]
		scanFuncBodies(pkg, f, filePath, func(from, fp string, body ast.Node) {
			scanCallsInNode(pkg, from, fp, body, onCall, onUnresolved)
		})
		scanCallGraphVarInits(pkg, f, filePath, onCall, onUnresolved)
	}
}

func (r *indexRun) makeCallEdgeRecorder(pkg *packages.Package) func(from, to, filePath, expr string, line, col int) {
	return func(from, to, filePath, expr string, line, col int) {
		if _, err := r.insertCall.Exec(
			r.moduleRoot, pkg.PkgPath, filePath, line, col, from, to, expr, "call",
		); err == nil {
			r.callCount++
		}
	}
}

func (r *indexRun) makeUnresolvedEdgeRecorder(pkg *packages.Package) func(from, filePath, expr string, line, col int) {
	return func(from, filePath, expr string, line, col int) {
		if _, err := r.insertUnresolved.Exec(
			r.moduleRoot, pkg.PkgPath, filePath, line, col, from, expr, "unresolved",
		); err == nil {
			r.unresolvedCount++
		}
	}
}

// scanCallGraphVarInits walks each package-scope var initializer, recording
// both the calls in the initializer expression itself and any calls inside
// nested FuncLit bodies (which need a synthetic from-fqname tied to position).
func scanCallGraphVarInits(pkg *packages.Package, f *ast.File, filePath string,
	onCall func(from, to, filePath, expr string, line, col int),
	onUnresolved func(from, filePath, expr string, line, col int),
) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			owner := "var"
			if len(vs.Names) > 0 {
				owner = vs.Names[0].Name
			}
			for _, value := range vs.Values {
				syntheticFrom := fmt.Sprintf("%s.init$var:%s", pkg.PkgPath, owner)
				scanCallsInNode(pkg, syntheticFrom, filePath, value, onCall, onUnresolved)
				scanFuncLitsInVarInit(pkg, owner, filePath, value, onCall, onUnresolved)
			}
		}
	}
}

func scanFuncLitsInVarInit(pkg *packages.Package, owner, filePath string, value ast.Node,
	onCall func(from, to, filePath, expr string, line, col int),
	onUnresolved func(from, filePath, expr string, line, col int),
) {
	ast.Inspect(value, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok || lit.Body == nil {
			return true
		}
		from := funcLitFQName(pkg.PkgPath, owner, pkg.Fset.PositionFor(lit.Type.Func, false))
		scanCallsInNode(pkg, from, filePath, lit.Body, onCall, onUnresolved)
		return false
	})
}

// scanFuncBodies iterates function declarations in a file, calling fn with
// the function's fqname, file path, and body for each.
func scanFuncBodies(pkg *packages.Package, f *ast.File, filePath string, fn func(from, filePath string, body ast.Node)) {
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		obj := pkg.TypesInfo.Defs[fd.Name]
		if obj == nil {
			continue
		}
		fn(objectFQName(obj), filePath, fd.Body)
	}
}

// scanVarInits iterates package-scope var initializers in a file, calling fn
// with a synthetic from_fqname, file path, and each initializer value.
func scanVarInits(pkg *packages.Package, f *ast.File, filePath string, fn func(from, filePath string, value ast.Node)) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			owner := "var"
			if len(vs.Names) > 0 {
				owner = vs.Names[0].Name
			}
			syntheticFrom := fmt.Sprintf("%s.init$var:%s", pkg.PkgPath, owner)
			for _, value := range vs.Values {
				fn(syntheticFrom, filePath, value)
			}
		}
	}
}

// resolveAndEmitRef resolves expr as a function-typed object and calls emit.
func resolveAndEmitRef(pkg *packages.Package, from, filePath string, expr ast.Expr, emit func(from, filePath, to, exprText string, pos token.Position)) {
	var argObj types.Object
	switch a := expr.(type) {
	case *ast.Ident:
		argObj = pkg.TypesInfo.Uses[a]
	case *ast.SelectorExpr:
		if sel := pkg.TypesInfo.Selections[a]; sel != nil {
			argObj = sel.Obj()
		} else {
			argObj = pkg.TypesInfo.Uses[a.Sel]
		}
	}
	if argObj == nil {
		return
	}
	if _, ok := argObj.Type().Underlying().(*types.Signature); !ok {
		return
	}
	to := objectFQName(argObj)
	if to == "" {
		return
	}
	pos := pkg.Fset.PositionFor(expr.Pos(), false)
	emit(from, filePath, to, exprString(expr), pos)
}

// scanRefsInNode walks an AST subtree for function-value refs and emits each.
func scanRefsInNode(pkg *packages.Package, from, filePath string, node ast.Node, emit func(from, filePath, to, exprText string, pos token.Position)) {
	if from == "" || node == nil {
		return
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			for _, arg := range v.Args {
				resolveAndEmitRef(pkg, from, filePath, arg, emit)
			}
		case *ast.CompositeLit:
			for _, elt := range v.Elts {
				var target ast.Expr
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					target = kv.Value
				} else {
					target = elt
				}
				resolveAndEmitRef(pkg, from, filePath, target, emit)
			}
		case *ast.AssignStmt:
			for _, rhs := range v.Rhs {
				resolveAndEmitRef(pkg, from, filePath, rhs, emit)
			}
		case *ast.ValueSpec:
			for _, val := range v.Values {
				resolveAndEmitRef(pkg, from, filePath, val, emit)
			}
		case *ast.ReturnStmt:
			for _, ret := range v.Results {
				resolveAndEmitRef(pkg, from, filePath, ret, emit)
			}
		case *ast.SendStmt:
			resolveAndEmitRef(pkg, from, filePath, v.Value, emit)
		}
		return true
	})
}

// indexRefs captures function values passed as arguments, composite lit values,
// assignment RHS, return values, and channel sends.
func (r *indexRun) indexRefs() error {
	insertRef, err := r.tx.Prepare(`
INSERT INTO calls(
	module_root, package_path, file_path, line, col, from_fqname, to_fqname, callee_expr, kind
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'ref')`)
	if err != nil {
		return err
	}
	defer insertRef.Close()

	for _, pkg := range r.pkgs {
		if pkg.Types == nil || pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
			continue
		}
		fileByPos := buildFileByPos(pkg)

		emit := func(from, filePath, to, exprText string, pos token.Position) {
			if _, err := insertRef.Exec(
				r.moduleRoot, pkg.PkgPath, filePath, pos.Line, pos.Column,
				from, to, exprText,
			); err != nil {
				log.Printf("warn: ref insert %s -> %s: %v", from, to, err)
			}
		}

		for _, f := range pkg.Syntax {
			filePath := fileByPos[f]
			scanFuncBodies(pkg, f, filePath, func(from, fp string, body ast.Node) {
				scanRefsInNode(pkg, from, fp, body, emit)
			})
			scanVarInits(pkg, f, filePath, func(from, fp string, value ast.Node) {
				scanRefsInNode(pkg, from, fp, value, emit)
			})
		}
	}
	return nil
}

// typeRefEmitter is called to record a single type reference.
type typeRefEmitter func(from, filePath, toFQ, refKind, exprText string, pos token.Position)

// emitTypedExpr resolves a type expression and emits a type reference if it
// has a named type with a package. Shared by most type-ref cases.
func emitTypedExpr(pkg *packages.Package, from, filePath string, expr ast.Expr, refKind string, emit typeRefEmitter) {
	tv, ok := pkg.TypesInfo.Types[expr]
	if !ok {
		return
	}
	if refKind == "conversion" && !tv.IsType() {
		return
	}
	fq := typeRefFQName(tv.Type)
	if fq == "" {
		return
	}
	pos := pkg.Fset.PositionFor(expr.Pos(), false)
	emit(from, filePath, fq, refKind, exprString(expr), pos)
}

// scanTypeSwitchCases emits type refs for each case clause in a type switch.
func scanTypeSwitchCases(pkg *packages.Package, from, filePath string, body *ast.BlockStmt, emit typeRefEmitter) {
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, caseExpr := range cc.List {
			emitTypedExpr(pkg, from, filePath, caseExpr, "type_switch", emit)
		}
	}
}

// scanTypeRefsInNode walks an AST subtree and emits type references.
func scanTypeRefsInNode(pkg *packages.Package, from, filePath string, node ast.Node, emit typeRefEmitter) {
	if from == "" || node == nil {
		return
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.TypeAssertExpr:
			if v.Type != nil {
				emitTypedExpr(pkg, from, filePath, v.Type, "type_assert", emit)
			}
		case *ast.TypeSwitchStmt:
			scanTypeSwitchCases(pkg, from, filePath, v.Body, emit)
			return false
		case *ast.CompositeLit:
			if v.Type != nil {
				emitTypedExpr(pkg, from, filePath, v.Type, "composite_lit", emit)
			}
		case *ast.CallExpr:
			emitTypedExpr(pkg, from, filePath, v.Fun, "conversion", emit)
		case *ast.SelectorExpr:
			if sel := pkg.TypesInfo.Selections[v]; sel != nil && sel.Kind() == types.FieldVal {
				if fq := typeRefFQName(sel.Recv()); fq != "" {
					pos := pkg.Fset.PositionFor(v.Sel.Pos(), false)
					emit(from, filePath, fq, "field_access", v.Sel.Name, pos)
				}
			}
		}
		return true
	})
}

// scanStructEmbeds scans type declarations for struct embeds and emits type refs.
func scanStructEmbeds(pkg *packages.Package, f *ast.File, filePath string, emit typeRefEmitter) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			typeObj := pkg.TypesInfo.Defs[ts.Name]
			if typeObj == nil {
				continue
			}
			fromFQ := objectFQName(typeObj)
			for _, field := range st.Fields.List {
				if len(field.Names) > 0 {
					continue
				}
				if tv, ok := pkg.TypesInfo.Types[field.Type]; ok {
					if fq := typeRefFQName(tv.Type); fq != "" {
						pos := pkg.Fset.PositionFor(field.Type.Pos(), false)
						emit(fromFQ, filePath, fq, "embed", exprString(field.Type), pos)
					}
				}
			}
		}
	}
}

// indexTypeRefs captures type assertions, type switches, composite lits,
// type conversions, field accesses, and struct embeds.
func (r *indexRun) indexTypeRefs() error {
	insertTypeRef, err := r.tx.Prepare(`
INSERT INTO type_refs(module_root, package_path, file_path, line, col,
    from_fqname, to_fqname, ref_kind, expr)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertTypeRef.Close()

	for _, pkg := range r.pkgs {
		if pkg.Types == nil || pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
			continue
		}
		fileByPos := buildFileByPos(pkg)

		emit := func(from, filePath, toFQ, refKind, exprText string, pos token.Position) {
			if from == "" || toFQ == "" {
				return
			}
			if _, err := insertTypeRef.Exec(
				r.moduleRoot, pkg.PkgPath, filePath, pos.Line, pos.Column,
				from, toFQ, refKind, exprText,
			); err == nil {
				r.typeRefCount++
			}
		}

		for _, f := range pkg.Syntax {
			filePath := fileByPos[f]
			scanFuncBodies(pkg, f, filePath, func(from, fp string, body ast.Node) {
				scanTypeRefsInNode(pkg, from, fp, body, emit)
			})
			scanVarInits(pkg, f, filePath, func(from, fp string, value ast.Node) {
				scanTypeRefsInNode(pkg, from, fp, value, emit)
			})
			scanStructEmbeds(pkg, f, filePath, emit)
		}
	}
	return nil
}

// writePackageMeta upserts package metadata and computes per-file hashes
// using a bounded worker pool.
func (r *indexRun) writePackageMeta() {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, pkg := range r.pkgs {
		if _, err := r.db.Exec(`INSERT INTO package_meta(module_root, package_path, indexed_at)
             VALUES (?, ?, ?)
             ON CONFLICT(module_root, package_path) DO UPDATE SET indexed_at=excluded.indexed_at`,
			r.moduleRoot, pkg.PkgPath, now); err != nil {
			log.Printf("warn: package_meta upsert %s/%s: %v", r.moduleRoot, pkg.PkgPath, err)
		}
	}

	type hashJob struct {
		pkgPath string
		file    string
	}
	var jobs []hashJob
	for _, pkg := range r.pkgs {
		for _, f := range pkg.CompiledGoFiles {
			jobs = append(jobs, hashJob{pkgPath: pkg.PkgPath, file: f})
		}
	}

	results := make([]fileResult, len(jobs))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func(idx int, j hashJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			h, err := hashFile(j.file)
			results[idx] = fileResult{path: j.file, hash: h, err: err}
		}(i, job)
	}
	wg.Wait()

	type pkgFileResult struct {
		files []fileResult
	}
	perPkg := make(map[string]*pkgFileResult)
	idx := 0
	for _, pkg := range r.pkgs {
		pfr := &pkgFileResult{}
		for range pkg.CompiledGoFiles {
			pfr.files = append(pfr.files, results[idx])
			idx++
		}
		perPkg[pkg.PkgPath] = pfr
	}

	for pkgPath, pfr := range perPkg {
		var sortedPaths []string
		for _, fr := range pfr.files {
			if fr.err != nil {
				log.Printf("warn: hash file %s: %v", fr.path, fr.err)
				continue
			}
			if _, err := r.db.Exec(`INSERT INTO package_files(module_root, package_path, file_path, file_hash)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(module_root, package_path, file_path) DO UPDATE SET file_hash=excluded.file_hash`,
				r.moduleRoot, pkgPath, fr.path, fr.hash); err != nil {
				log.Printf("warn: package_files insert %s: %v", fr.path, err)
			}
			sortedPaths = append(sortedPaths, fr.path)
		}
		sort.Strings(sortedPaths)
		filesHash, err := ComputeFilesHash(sortedPaths)
		if err != nil {
			log.Printf("warn: compute files_hash %s/%s: %v", r.moduleRoot, pkgPath, err)
			continue
		}
		if _, err := r.db.Exec(`UPDATE package_meta SET files_hash = ? WHERE module_root = ? AND package_path = ?`,
			filesHash, r.moduleRoot, pkgPath); err != nil {
			log.Printf("warn: package_meta files_hash update %s/%s: %v", r.moduleRoot, pkgPath, err)
		}
	}
}

// prepareStatements prepares all SQL statements for the indexing transaction.
func (r *indexRun) prepareStatements() error {
	var err error
	r.insertSymbol, err = r.tx.Prepare(`
INSERT INTO symbols(
	module_root, package_path, package_name, file_path, name, kind, recv, signature, fqname, exported, line, col
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	r.insertCall, err = r.tx.Prepare(`
INSERT INTO calls(
	module_root, package_path, file_path, line, col, from_fqname, to_fqname, callee_expr, kind
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	r.insertUnresolved, err = r.tx.Prepare(`
INSERT INTO unresolved_calls(
	module_root, package_path, file_path, line, col, from_fqname, expr, reason
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	r.insertImpl, err = r.tx.Prepare(`
INSERT INTO implements(module_root, iface_pkg, iface_fqname, impl_pkg, impl_fqname, is_pointer)
VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	r.insertImplMethod, err = r.tx.Prepare(`
INSERT OR IGNORE INTO impl_methods(module_root, impl_fqname)
VALUES (?, ?)`)
	if err != nil {
		return err
	}
	r.insertIfaceMethod, err = r.tx.Prepare(`
INSERT OR IGNORE INTO iface_methods(module_root, iface_fqname, method_name)
VALUES (?, ?, ?)`)
	return err
}

// closeStatements closes all prepared statements.
func (r *indexRun) closeStatements() {
	for _, s := range []*sql.Stmt{r.insertSymbol, r.insertCall, r.insertUnresolved, r.insertImpl, r.insertImplMethod, r.insertIfaceMethod} {
		if s != nil {
			s.Close()
		}
	}
}

// loadPackages loads and validates Go packages for indexing.
func loadPackages(moduleRoot string, enableCGO, withTests bool) ([]*packages.Package, bool, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo,
		Dir:   moduleRoot,
		Tests: withTests,
	}
	if enableCGO {
		cfg.Env = append(os.Environ(), "CGO_ENABLED=1")
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, false, err
	}
	if withTests {
		pkgs = deduplicateTestPackages(pkgs)
	}
	var hadPkgErr bool
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			hadPkgErr = true
			for _, pe := range p.Errors {
				log.Printf("warn: %s: %s", moduleRoot, pe)
			}
		}
	}
	if len(pkgs) == 0 {
		return nil, false, fmt.Errorf("no packages")
	}
	log.Printf("  loaded %d package(s)", len(pkgs))
	return pkgs, hadPkgErr, nil
}

// IndexModule loads all packages under moduleRoot, inserts symbols, calls,
// unresolved calls, interface-satisfaction rows, and type-reference rows into db.
// Returns (symbolCount, callCount, unresolvedCount, typeRefCount, error).
func IndexModule(db *sql.DB, moduleRoot string, enableCGO bool, withTests bool) (int, int, int, int, error) {
	pkgs, hadPkgErr, err := loadPackages(moduleRoot, enableCGO, withTests)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	if _, err := db.Exec(`INSERT OR IGNORE INTO modules(root) VALUES (?)`, moduleRoot); err != nil {
		return 0, 0, 0, 0, err
	}
	if err := PurgeModule(db, moduleRoot); err != nil {
		return 0, 0, 0, 0, err
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer tx.Rollback()

	r := &indexRun{db: db, tx: tx, moduleRoot: moduleRoot, pkgs: pkgs}
	if err := r.prepareStatements(); err != nil {
		return 0, 0, 0, 0, err
	}
	defer r.closeStatements()

	r.indexPackageSymbols()
	r.indexCallGraph()

	if err := r.indexRefs(); err != nil {
		return 0, 0, 0, 0, err
	}

	implCount, implErr := IndexImplements(r.insertImpl, r.insertImplMethod, r.insertIfaceMethod, r.pkgs, r.moduleRoot)
	if implErr != nil {
		log.Printf("warn: index implements for %s: %v", moduleRoot, implErr)
	} else {
		log.Printf("  implements: %d pairs", implCount)
	}

	if err := r.indexTypeRefs(); err != nil {
		return 0, 0, 0, 0, err
	}
	log.Printf("  type_refs: %d", r.typeRefCount)

	if err := tx.Commit(); err != nil {
		return 0, 0, 0, 0, err
	}

	r.writePackageMeta()

	if hadPkgErr {
		return r.symbolCount, r.callCount, r.unresolvedCount, r.typeRefCount, fmt.Errorf("indexed with package load/type errors")
	}
	return r.symbolCount, r.callCount, r.unresolvedCount, r.typeRefCount, nil
}

// deduplicateTestPackages filters the package list returned by packages.Load
// when Tests=true, removing duplicates caused by the test-augmented loader:
//
//  1. The generated test-binary package (ID ends in ".test", no "[") contains
//     auto-generated symbols (benchmarks, test main) and must not be indexed.
//
//  2. When a package has _test.go files in the same package namespace, the
//     loader returns both the normal variant (ID == PkgPath) and a
//     test-augmented variant (ID contains "["). The normal variant is a strict
//     subset of the test variant, so we drop it to avoid duplicate symbols.
func deduplicateTestPackages(pkgs []*packages.Package) []*packages.Package {
	// Collect PkgPaths that have a test-augmented variant.
	hasTestVariant := make(map[string]bool, len(pkgs))
	for _, pkg := range pkgs {
		if strings.Contains(pkg.ID, "[") {
			hasTestVariant[pkg.PkgPath] = true
		}
	}
	result := pkgs[:0:0]
	for _, pkg := range pkgs {
		// Skip generated test-binary packages: ID ends in ".test" (no "[").
		if !strings.Contains(pkg.ID, "[") && strings.HasSuffix(pkg.ID, ".test") {
			continue
		}
		// Skip the normal variant when a test-augmented variant exists.
		if !strings.Contains(pkg.ID, "[") && hasTestVariant[pkg.PkgPath] {
			continue
		}
		result = append(result, pkg)
	}
	return result
}

type ifaceEntry struct {
	fqname string
	pkg    string
	typ    *types.Named
	iface  *types.Interface
}

type concreteEntry struct {
	fqname string
	pkg    string
	typn   *types.Named
	typ    types.Type
	ptr    *types.Pointer
}

// collectIfacesAndConcretes scans all packages and classifies named types.
func collectIfacesAndConcretes(pkgs []*packages.Package) ([]ifaceEntry, []concreteEntry) {
	var ifaces []ifaceEntry
	var concretes []concreteEntry
	for _, pkg := range pkgs {
		if pkg.Types == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			base := types.Unalias(tn.Type())
			named, ok := base.(*types.Named)
			if !ok {
				continue
			}
			fqname := fmt.Sprintf("%s.%s", pkg.PkgPath, name)
			if iface, ok := named.Underlying().(*types.Interface); ok {
				if iface.NumMethods() == 0 {
					continue
				}
				ifaces = append(ifaces, ifaceEntry{
					fqname: fqname,
					pkg:    pkg.PkgPath,
					typ:    named,
					iface:  iface.Complete(),
				})
			} else {
				concretes = append(concretes, concreteEntry{
					fqname: fqname,
					pkg:    pkg.PkgPath,
					typn:   named,
					typ:    tn.Type(),
					ptr:    types.NewPointer(tn.Type()),
				})
			}
		}
	}
	return ifaces, concretes
}

// recordImplMethods records the concrete method fqnames that satisfy each
// interface method for method-precise dead suppression.
func recordImplMethods(insertImplMethod *sql.Stmt, moduleRoot string, implTyp types.Type, iface *types.Interface) error {
	mset := types.NewMethodSet(implTyp)
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		var lookupPkg *types.Package
		if !m.Exported() {
			lookupPkg = m.Pkg()
		}
		sel := mset.Lookup(lookupPkg, m.Name())
		if sel == nil {
			continue
		}
		fn, ok := sel.Obj().(*types.Func)
		if !ok {
			continue
		}
		fqname := objectFQName(fn)
		if fqname == "" {
			continue
		}
		if _, err := insertImplMethod.Exec(moduleRoot, fqname); err != nil {
			return err
		}
	}
	return nil
}

// IndexImplements populates the implements and impl_methods tables. For every
// non-empty interface I and every named concrete type T found across all loaded
// packages, it records a row in implements if T (or *T) satisfies I, and records
// the exact concrete method fqnames that satisfy each interface method in
// impl_methods. Generic types and the empty interface are skipped.
func IndexImplements(insertImpl, insertImplMethod, insertIfaceMethod *sql.Stmt, pkgs []*packages.Package, moduleRoot string) (int, error) {
	ifaces, concretes := collectIfacesAndConcretes(pkgs)

	for _, iface := range ifaces {
		for i := 0; i < iface.iface.NumMethods(); i++ {
			m := iface.iface.Method(i)
			if _, err := insertIfaceMethod.Exec(moduleRoot, iface.fqname, m.Name()); err != nil {
				return 0, err
			}
		}
	}

	count := 0
	for _, iface := range ifaces {
		for _, concrete := range concretes {
			valueType, ifaceType, ok := comparableImplementsTypes(concrete, iface)
			if !ok {
				continue
			}
			valueOk := safeImplements(valueType, ifaceType)
			ptrOk := !valueOk && safeImplements(types.NewPointer(valueType), ifaceType)
			if !valueOk && !ptrOk {
				continue
			}
			isPtr := 0
			if ptrOk {
				isPtr = 1
			}
			if _, err := insertImpl.Exec(moduleRoot, iface.pkg, iface.fqname, concrete.pkg, concrete.fqname, isPtr); err != nil {
				return count, err
			}
			count++

			implTyp := valueType
			if ptrOk {
				implTyp = types.NewPointer(valueType)
			}
			if err := recordImplMethods(insertImplMethod, moduleRoot, implTyp, ifaceType); err != nil {
				return count, err
			}
		}
	}
	return count, nil
}

func comparableImplementsTypes(concrete concreteEntry, iface ifaceEntry) (types.Type, *types.Interface, bool) {
	if iface.typ.TypeParams().Len() == 0 && concrete.typn.TypeParams().Len() == 0 {
		return concrete.typ, iface.iface, true
	}
	if iface.typ.TypeParams().Len() != concrete.typn.TypeParams().Len() {
		return nil, nil, false
	}
	targs := make([]types.Type, 0, iface.typ.TypeParams().Len())
	for i := 0; i < iface.typ.TypeParams().Len(); i++ {
		targs = append(targs, iface.typ.TypeParams().At(i))
	}
	instIface, err := types.Instantiate(nil, iface.typ, targs, false)
	if err != nil {
		return nil, nil, false
	}
	instConcrete, err := types.Instantiate(nil, concrete.typn, targs, false)
	if err != nil {
		return nil, nil, false
	}
	out, ok := instIface.Underlying().(*types.Interface)
	if !ok {
		return nil, nil, false
	}
	return instConcrete, out.Complete(), true
}

// safeImplements wraps types.Implements in a recover to guard against panics
// from partially-loaded or unexpectedly-shaped types.
func safeImplements(v types.Type, iface *types.Interface) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	return types.Implements(v, iface)
}

func resolveCallTarget(info *types.Info, fun ast.Expr) types.Object {
	switch v := fun.(type) {
	case *ast.Ident:
		return info.Uses[v]
	case *ast.SelectorExpr:
		if sel := info.Selections[v]; sel != nil {
			return sel.Obj()
		}
		return info.Uses[v.Sel]
	case *ast.IndexExpr:
		return resolveCallTarget(info, v.X)
	case *ast.IndexListExpr:
		return resolveCallTarget(info, v.X)
	case *ast.ParenExpr:
		return resolveCallTarget(info, v.X)
	default:
		return nil
	}
}

func objectFQName(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			recv := compactRecv(typeString(sig.Recv().Type()), obj.Pkg().Path())
			return fmt.Sprintf("%s.%s.%s", obj.Pkg().Path(), recv, fn.Name())
		}
	}
	return fmt.Sprintf("%s.%s", obj.Pkg().Path(), obj.Name())
}

func typeString(t types.Type) string {
	if t == nil {
		return ""
	}
	return types.TypeString(t, func(p *types.Package) string {
		if p == nil {
			return ""
		}
		return p.Path()
	})
}

func compactRecv(recv, pkgPath string) string {
	return strings.ReplaceAll(recv, pkgPath+".", "")
}

func funcLitFQName(pkgPath, owner string, pos token.Position) string {
	base := filepath.Base(pos.Filename)
	if owner == "" {
		owner = "var"
	}
	return fmt.Sprintf("%s.%s$lit@%s:%d:%d", pkgPath, owner, base, pos.Line, pos.Column)
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.IndexExpr:
		return exprString(v.X) + "[...]"
	case *ast.IndexListExpr:
		return exprString(v.X) + "[...]"
	case *ast.ParenExpr:
		return "(" + exprString(v.X) + ")"
	case *ast.CallExpr:
		// Chained call: the callee is itself a call expression (e.g. f()() or f()()()).
		// Record as "return value of <inner>()" so the unresolved entry is human-readable.
		return "return-value-of(" + exprString(v.Fun) + ")"
	case *ast.StarExpr:
		return "*" + exprString(v.X)
	case *ast.UnaryExpr:
		return fmt.Sprintf("%s%s", v.Op, exprString(v.X))
	default:
		return fmt.Sprintf("<%T>", e)
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// typeRefFQName returns the fqname of the named type underlying t,
// or "" for unnamed/universe-scope types (string, int, error, etc.).
func typeRefFQName(t types.Type) string {
	t = derefType(t)
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	return fmt.Sprintf("%s.%s", obj.Pkg().Path(), obj.Name())
}

func derefType(t types.Type) types.Type {
	for {
		p, ok := t.(*types.Pointer)
		if !ok {
			return t
		}
		t = p.Elem()
	}
}
