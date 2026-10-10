package config

var SnakeCase = snakeCase

func SecretFieldNames() []string {
	names := make([]string, 0, len(secretFields))
	for name := range secretFields {
		names = append(names, name)
	}
	return names
}
