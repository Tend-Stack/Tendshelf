package align

import "github.com/Tend-Stack/tendshelf/internal/jobs"

// BookKindForTest exposes the job kind to the external test package.
func (s *Service) BookKindForTest() jobs.Kind { return s.bookKind() }
