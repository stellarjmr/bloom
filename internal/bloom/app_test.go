package bloom

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type pathRunner map[string]bool

func (r pathRunner) LookPath(file string) (string, error) {
	if r[file] {
		return "/bin/" + file, nil
	}
	return "", errors.New("not found")
}

func (r pathRunner) Run(context.Context, string, ...string) CommandOutput {
	return CommandOutput{}
}

func TestFilterRunnableTasksSkipsDisabledAndMissingCommands(t *testing.T) {
	tasks := []Task{
		{Name: "brew", Enabled: true, RequiredCommand: "brew"},
		{Name: "yazi", Enabled: true, RequiredCommand: "ya"},
		{Name: "npm", Enabled: false, RequiredCommand: "npm"},
	}

	gotTasks := filterRunnableTasks(tasks, pathRunner{"brew": true})
	got := []string{}
	for _, task := range gotTasks {
		got = append(got, task.Name)
	}
	want := []string{"brew"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
