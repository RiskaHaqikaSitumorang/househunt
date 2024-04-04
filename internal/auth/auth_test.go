package auth_test

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
