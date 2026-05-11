package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type rowKey struct {
	contract string
	method   string
}

func validHeaderLine(line string) bool {
	return strings.EqualFold(line, "contract,method,totalExecutionTime") ||
		strings.EqualFold(line, "contract,method,totalCallMethodTime")
}

// parseTimeFile 返回每个 (contract, method) 对应的时间；跳过表头行与时间为 0 的项。
func parseTimeFile(path string) (map[rowKey]float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stats := make(map[rowKey]float64)
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if lineNo == 1 && validHeaderLine(line) {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			return nil, fmt.Errorf("%s:%d invalid line format: %q", path, lineNo, line)
		}
		contract := strings.TrimSpace(parts[0])
		method := strings.TrimSpace(parts[1])
		timeStr := strings.TrimSpace(parts[2])
		execTime, err := strconv.ParseFloat(timeStr, 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d invalid time value %q: %w", path, lineNo, timeStr, err)
		}

		if execTime == 0 {
			continue
		}

		stats[rowKey{contract: contract, method: method}] = execTime
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return stats, nil
}

func autoPickTwoTxt(dir string) (string, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".txt") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)
	if len(files) < 2 {
		return "", "", errors.New("目录下至少需要两个 .txt 文件")
	}
	return files[0], files[1], nil
}

func unionRows(a, b map[rowKey]float64) []rowKey {
	set := make(map[rowKey]struct{})
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	out := make([]rowKey, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].contract != out[j].contract {
			return out[i].contract < out[j].contract
		}
		return out[i].method < out[j].method
	})
	return out
}

func main() {
	dir := flag.String("dir", ".", "包含两个结果 txt 的目录")
	base := flag.String("base", "", "基线文件路径（可选）")
	target := flag.String("target", "", "对比文件路径（可选）")
	flag.Parse()

	var baseFile, targetFile string
	var err error
	if strings.TrimSpace(*base) != "" && strings.TrimSpace(*target) != "" {
		baseFile = *base
		targetFile = *target
	} else {
		baseFile, targetFile, err = autoPickTwoTxt(*dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "自动选择文件失败: %v\n", err)
			os.Exit(1)
		}
	}

	baseStats, err := parseTimeFile(baseFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取基线文件失败: %v\n", err)
		os.Exit(1)
	}
	targetStats, err := parseTimeFile(targetFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取对比文件失败: %v\n", err)
		os.Exit(1)
	}

	rows := unionRows(baseStats, targetStats)

	fmt.Printf("基线文件: %s\n", baseFile)
	fmt.Printf("对比文件: %s\n\n", targetFile)
	fmt.Println("contract,method,base_time,target_time,change_ratio")

	for _, k := range rows {
		b, hasBase := baseStats[k]
		t, hasTarget := targetStats[k]
		switch {
		case hasBase && hasTarget:
			changeRatio := (t - b) / b * 100
			fmt.Printf("%s,%s,%.6f,%.6f,%+.2f%%\n", k.contract, k.method, b, t, changeRatio)
		case hasBase:
			fmt.Printf("%s,%s,%.6f,NA,NA\n", k.contract, k.method, b)
		default:
			fmt.Printf("%s,%s,NA,%.6f,NA\n", k.contract, k.method, t)
		}
	}
}
