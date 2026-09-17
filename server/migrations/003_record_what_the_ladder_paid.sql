-- What the ladder actually paid, per seat, signed.
--
-- The verification worker reverses a match that fails re-simulation, and it
-- cannot recompute the amount: lpChange is a function of both ratings *at the
-- time*, and by the time a job runs they have moved. The loser's side is not
-- the winner's negated either — a loss is floored at the tier boundary, so the
-- two numbers genuinely differ and both have to be recorded.
--
-- Null for a match nobody has settled yet, and for every casual match, which
-- pays nothing by design.
ALTER TABLE matches
    ADD COLUMN p1_lp_change INTEGER,
    ADD COLUMN p2_lp_change INTEGER;
