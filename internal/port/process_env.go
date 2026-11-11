// SPDX-License-Identifier: GPL-3.0-or-later

package port

// ProcessEnv is the UI process's view of its environment. Adapters that start
// child processes read the environment through it instead of calling the os
// package, so tests can supply a fixed environment.
type ProcessEnv interface {
	// Environ returns the environment as "KEY=value" strings, like os.Environ.
	Environ() []string
	// LookupEnv returns the value of key and whether it is set.
	LookupEnv(key string) (string, bool)
	// HomeDir returns the current user's home directory.
	HomeDir() (string, error)
}
