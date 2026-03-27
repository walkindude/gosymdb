package alpha

type Store struct{}

func (s *Store) AddObservation(msg string) string {
	return msg
}

func Top(s *Store) string {
	return s.AddObservation("x")
}

const Answer = 42

var Global = "g"
