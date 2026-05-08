package gate

func buildCommand(projectType ProjectType) (commandSpec, bool) {
	switch projectType {
	case ProjectTypeGo:
		return commandSpec{name: "go", args: []string{"build", "./..."}}, true
	case ProjectTypeNode:
		return commandSpec{name: "npm", args: []string{"run", "build", "--if-present"}}, true
	case ProjectTypePython:
		return commandSpec{name: "python", args: []string{"-m", "compileall", "."}}, true
	case ProjectTypeRust:
		return commandSpec{name: "cargo", args: []string{"build"}}, true
	default:
		return commandSpec{}, false
	}
}
