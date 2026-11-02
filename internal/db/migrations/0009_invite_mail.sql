-- 0009_invite_mail: the job that emails an invite, so the Share page can say Sending, Sent or Couldn't send (P3-8).
ALTER TABLE invites ADD COLUMN mail_job_id TEXT;
