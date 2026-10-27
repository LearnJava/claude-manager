package learned

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Folder returns the lessons folder of a project root.
func Folder(root string) string { return filepath.Join(root, filepath.FromSlash(Dir)) }

// ReadFiles reads every lesson file of the project. A file that cannot be read
// as a lesson, or repeats a key already read, is returned in problems ("name:
// reason") and left alone. A missing folder is no files, not an error.
func ReadFiles(root string) (lessons []Lesson, problems []string, err error) {
	dir := Folder(root)
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	seen := map[Ref]string{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if fi, err := e.Info(); err == nil && fi.Size() > maxFileBytes {
			problems = append(problems, fmt.Sprintf("%s: file is larger than %d bytes", e.Name(), maxFileBytes))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		l, err := Parse(data)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		if first, dup := seen[l.Ref()]; dup {
			problems = append(problems, fmt.Sprintf("%s: same %s %q as %s", e.Name(), l.Kind, l.Key, first))
			continue
		}
		seen[l.Ref()] = e.Name()
		l.Path = path
		lessons = append(lessons, l)
	}
	sort.Slice(lessons, func(i, j int) bool { return lessons[i].Path < lessons[j].Path })
	return lessons, problems, nil
}

// WriteFile writes the lesson into the lessons folder (tmp → rename) under its
// canonical name, creating the folder. If the lesson was read from a file with
// another name, that file is removed so the key stays in one place.
func WriteFile(root string, l Lesson) error {
	dir := Folder(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, FileName(l.Kind, l.Key))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, l.Marshal(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if l.Path != "" && !samePath(l.Path, path) {
		_ = removeInside(dir, l.Path)
	}
	return nil
}

// RemoveFile deletes the lesson's file, provided it is inside the lessons folder.
func RemoveFile(root string, l Lesson) error {
	path := l.Path
	if path == "" {
		path = filepath.Join(Folder(root), FileName(l.Kind, l.Key))
	}
	return removeInside(Folder(root), path)
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func removeInside(dir, path string) error {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.ContainsRune(rel, filepath.Separator) {
		return fmt.Errorf("learned: %s is not inside %s", path, dir)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
