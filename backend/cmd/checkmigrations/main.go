// checkmigrations 是发版前的本地体检工具：把本地迁移文件校验和跟生产库
// schema_migrations 表里记录的校验和逐条比对，在构建新二进制之前就找出 233
// 那种"文件内容变了但数据库还记着旧校验和"的问题——晚了的话，新二进制会在
// 生产上启动时直接崩溃退出（参见 2026-09-06 的一次发版事故）。
// 校验和算法必须跟 internal/repository/migrations_runner.go 的
// ApplyMigrations 完全一致（sha256 of strings.TrimSpace(content)），否则报
// 出来的差异是假的。已经写进 migrationChecksumCompatibilityRules 白名单的历史
// 差异会标成 WHITELISTED（部署不受影响），只有 MISMATCH 才需要处理。
//
// 用法（B1 构建步骤之前，在 backend/ 目录下）：
//
//	ssh -i "$PRODKEY" ec2-user@$PRODIP "sudo docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc \"select filename || '|' || checksum from schema_migrations order by filename;\"" > /tmp/prod_migration_checksums.txt
//	go run ./cmd/checkmigrations migrations /tmp/prod_migration_checksums.txt
//
// 第二个参数每行格式为 "filename|checksum"。
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/repository"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: checkmigrations <migrations-dir> <prod_checksums.txt>")
		os.Exit(2)
	}

	prod, err := loadProdChecksums(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "load prod checksums:", err)
		os.Exit(1)
	}

	local, err := loadLocalChecksums(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "load local checksums:", err)
		os.Exit(1)
	}

	names := make([]string, 0, len(prod))
	for name := range prod {
		names = append(names, name)
	}
	sort.Strings(names)

	mismatchCount := 0
	for _, name := range names {
		dbSum := prod[name]
		fileSum, ok := local[name]
		if !ok {
			fmt.Printf("MISSING_LOCALLY  %s  (db=%s)\n", name, dbSum)
			continue
		}
		if dbSum == fileSum {
			continue
		}
		if repository.IsMigrationChecksumCompatible(name, dbSum, fileSum) {
			fmt.Printf("WHITELISTED  %s  (已在 migrationChecksumCompatibilityRules 里覆盖，部署时不会报错)\n", name)
			continue
		}
		mismatchCount++
		fmt.Printf("MISMATCH  %s  ← 需要处理：要么恢复原文件，要么按同样的写法把这两个 checksum 加进\n"+
			"          internal/repository/migrations_runner.go 的 migrationChecksumCompatibilityRules，\n"+
			"          否则新二进制在生产上启动时会直接崩溃退出\n"+
			"  db=%s\n  file=%s\n", name, dbSum, fileSum)
	}
	fmt.Printf("\nchecked %d production rows, %d local files, %d unresolved mismatches\n", len(prod), len(local), mismatchCount)
	if mismatchCount > 0 {
		os.Exit(1)
	}
}

func loadProdChecksums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		result[parts[0]] = parts[1]
	}
	return result, scanner.Err()
}

func loadLocalChecksums(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue
		}
		sum := sha256.Sum256([]byte(trimmed))
		result[entry.Name()] = hex.EncodeToString(sum[:])
	}
	return result, nil
}
