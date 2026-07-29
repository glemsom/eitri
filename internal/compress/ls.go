package compress

import (
	"fmt"
	"strconv"
	"strings"
)

// compressLs applies ls output compression.
func compressLs(output string) *string {
	lines := strings.Split(output, "\n")
	if len(lines) < 5 {
		return nil
	}

	// Detect long format: lines starting with permissions (-, d, l) or "total".
	isLong := false
	for _, line := range lines {
		tr := strings.TrimSpace(line)
		if tr == "" {
			continue
		}
		if tr == "." || tr == ".." {
			continue
		}
		if strings.HasPrefix(tr, "total ") {
			isLong = true
			break
		}
		if len(tr) > 0 && (tr[0] == '-' || tr[0] == 'd' || tr[0] == 'l') {
			isLong = true
			break
		}
	}

	if isLong {
		return compressLong(lines)
	}
	return compressShort(lines)
}

// compressLong compresses `ls -la` style output.
func compressLong(lines []string) *string {
	var dirs, files []string

	for _, line := range lines {
		tr := strings.TrimSpace(line)
		if tr == "" || tr == "." || tr == ".." {
			continue
		}
		if strings.HasPrefix(tr, "total ") {
			continue
		}

		// Split by whitespace. Long format has at least 9 columns:
		//  permissions links owner group size month day time name
		parts := strings.Fields(tr)
		if len(parts) < 9 {
			continue
		}

		name := strings.Join(parts[8:], " ")
		if name == "." || name == ".." {
			continue
		}

		if parts[0][0] == 'd' {
			dirs = append(dirs, name+"/")
		} else {
			size := formatSize(parts[4])
			files = append(files, fmt.Sprintf("%s  %s", name, size))
		}
	}

	if len(dirs)+len(files) == 0 {
		return nil
	}

	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(f)
		sb.WriteByte('\n')
	}
	if len(files) > 0 && len(dirs) > 0 {
		sb.WriteByte('\n')
	}
	for _, d := range dirs {
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	sb.WriteString(fmt.Sprintf("\n%d files, %d dirs", len(files), len(dirs)))

	result := sb.String()
	return &result
}

// compressShort compresses plain `ls` output.
func compressShort(lines []string) *string {
	var items []string
	for _, line := range lines {
		tr := strings.TrimSpace(line)
		if tr == "" {
			continue
		}
		// Short ls may have multiple items per line.
		items = append(items, strings.Fields(tr)...)
	}

	if len(items) < 10 {
		return nil
	}

	var dirs, files []string
	for _, item := range items {
		if strings.HasSuffix(item, "/") {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}

	var sb strings.Builder
	for _, d := range dirs {
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	if len(dirs) > 0 && len(files) > 0 {
		sb.WriteByte('\n')
	}

	// Pack files into 70-char-wide rows.
	var lineBuf strings.Builder
	for _, f := range files {
		if lineBuf.Len() > 0 && lineBuf.Len()+len(f)+2 > 70 {
			sb.WriteString(lineBuf.String())
			sb.WriteByte('\n')
			lineBuf.Reset()
		}
		if lineBuf.Len() > 0 {
			lineBuf.WriteString("  ")
		}
		lineBuf.WriteString(f)
	}
	if lineBuf.Len() > 0 {
		sb.WriteString(lineBuf.String())
		sb.WriteByte('\n')
	}

	sb.WriteString(fmt.Sprintf("\n%d files, %d dirs", len(files), len(dirs)))
	result := sb.String()
	return &result
}

// formatSize converts a raw byte-count string to a human-readable form.
func formatSize(raw string) string {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return raw
	}
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fG", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}
