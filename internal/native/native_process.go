package native

import (
	"fmt"

	"geblang/internal/runtime"
)

func putString(d runtime.Dict, key, value string) {
	k := runtime.String{Value: key}
	d.PutEntry(DictKey(k), runtime.DictEntry{Key: k, Value: runtime.String{Value: value}})
}

func putInt(d runtime.Dict, key string, value int64) {
	k := runtime.String{Value: key}
	d.PutEntry(DictKey(k), runtime.DictEntry{Key: k, Value: runtime.NewInt64(value)})
}

func singlePid(args []runtime.Value, label string) (int, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects a single pid argument", label)
	}
	n, ok := AsInt64(args[0])
	if !ok {
		return 0, fmt.Errorf("%s pid must be an int", label)
	}
	return int(n), nil
}

func registerProcess(r *Registry) {
	r.Register("process", "pid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.pid expects no arguments")
		}
		return runtime.NewInt64(int64(sysPid())), nil
	})
	r.Register("process", "ppid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.ppid expects no arguments")
		}
		return runtime.NewInt64(int64(procPpid())), nil
	})
	r.Register("process", "uid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.uid expects no arguments")
		}
		return procCred(procUID)
	})
	r.Register("process", "gid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.gid expects no arguments")
		}
		return procCred(procGID)
	})
	r.Register("process", "euid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.euid expects no arguments")
		}
		return procCred(procEUID)
	})
	r.Register("process", "egid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.egid expects no arguments")
		}
		return procCred(procEGID)
	})
	r.Register("process", "groups", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.groups expects no arguments")
		}
		gids, err := procGroups()
		if err != nil {
			return nil, err
		}
		elems := make([]runtime.Value, len(gids))
		for i, g := range gids {
			elems[i] = runtime.NewInt64(int64(g))
		}
		return &runtime.List{Elements: elems}, nil
	})
	r.Register("process", "list", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("process.list expects no arguments")
		}
		infos, err := procList()
		if err != nil {
			return nil, err
		}
		elems := make([]runtime.Value, len(infos))
		for i, info := range infos {
			elems[i] = procInfoDict(info)
		}
		return &runtime.List{Elements: elems}, nil
	})
	r.Register("process", "info", func(args []runtime.Value) (runtime.Value, error) {
		pid, err := singlePid(args, "process.info")
		if err != nil {
			return nil, err
		}
		info, ok, err := procInfo(pid)
		if err != nil {
			return nil, err
		}
		if !ok {
			return runtime.Null{}, nil
		}
		return procInfoDict(info), nil
	})
	r.Register("process", "exists", func(args []runtime.Value) (runtime.Value, error) {
		pid, err := singlePid(args, "process.exists")
		if err != nil {
			return nil, err
		}
		ok, err := procExists(pid)
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: ok}, nil
	})
	r.Register("process", "setuid", func(args []runtime.Value) (runtime.Value, error) {
		uid, err := singlePid(args, "process.setuid")
		if err != nil {
			return nil, err
		}
		if err := requireProcessControl("process.setuid"); err != nil {
			return nil, err
		}
		if err := procSetuid(uid); err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	})
	r.Register("process", "setgid", func(args []runtime.Value) (runtime.Value, error) {
		gid, err := singlePid(args, "process.setgid")
		if err != nil {
			return nil, err
		}
		if err := requireProcessControl("process.setgid"); err != nil {
			return nil, err
		}
		if err := procSetgid(gid); err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	})
	r.Register("process", "kill", func(args []runtime.Value) (runtime.Value, error) {
		pid, err := singlePid(args, "process.kill")
		if err != nil {
			return nil, err
		}
		if err := requireProcessControl("process.kill"); err != nil {
			return nil, err
		}
		if err := procSignal(pid, "KILL"); err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	})
	r.Register("process", "signal", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("process.signal expects a pid and a signal name")
		}
		pidN, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("process.signal pid must be an int")
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("process.signal name must be a string")
		}
		if err := requireProcessControl("process.signal"); err != nil {
			return nil, err
		}
		if err := procSignal(int(pidN), name.Value); err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	})

	r.Register("sys", "osVersion", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.osVersion expects no arguments")
		}
		v, err := sysOSVersion()
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: v}, nil
	})
}

// processInfo is the platform-neutral shape filled by procList / procInfo.
type processInfo struct {
	pid     int
	ppid    int
	name    string
	cmdline string
	state   string
}

func procInfoDict(info processInfo) runtime.Dict {
	d := runtime.NewDict()
	putInt(d, "pid", int64(info.pid))
	putInt(d, "ppid", int64(info.ppid))
	putString(d, "name", info.name)
	putString(d, "cmdline", info.cmdline)
	putString(d, "state", info.state)
	return d
}

func procCred(fn func() (int, error)) (runtime.Value, error) {
	id, err := fn()
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(id)), nil
}
