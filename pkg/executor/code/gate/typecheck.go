package gate

func typeCheckCommand(projectType ProjectType) (commandSpec, bool) {
	switch projectType {
	case ProjectTypeGo:
		return commandSpec{name: "go", args: []string{"vet", "./..."}}, true
	case ProjectTypeNode:
		return commandSpec{name: "npm", args: []string{"run", "typecheck", "--if-present"}}, true
	case ProjectTypePython:
		return commandSpec{name: "python", args: []string{"-m", "compileall", "."}}, true
	case ProjectTypeRust:
		return commandSpec{name: "cargo", args: []string{"check"}}, true
	default:
		return commandSpec{}, false
	}
}
