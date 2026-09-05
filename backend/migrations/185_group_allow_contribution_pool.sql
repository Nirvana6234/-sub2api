ALTER TABLE groups
  ADD COLUMN IF NOT EXISTS allow_contribution_pool boolean NOT NULL DEFAULT false;
