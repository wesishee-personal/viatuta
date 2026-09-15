-- +goose Up

-- Turn costs need to know which way an edge points where it meets a junction.
--
-- The compass bearing at each END of the edge is stored, not one bearing for
-- the whole edge, because roads curve: a segment can enter a junction heading
-- north and leave the previous one heading east. Using a single chord bearing
-- would misclassify turns on exactly the curved, complex junctions where
-- getting it right matters most.
--
-- Both are degrees clockwise from north, in the direction of travel.
ALTER TABLE routing_edge
    ADD COLUMN bearing_start real,
    ADD COLUMN bearing_end   real;

COMMENT ON COLUMN routing_edge.bearing_start IS
    'Compass bearing (deg from north) leaving from_node, in travel direction';
COMMENT ON COLUMN routing_edge.bearing_end IS
    'Compass bearing (deg from north) arriving at to_node, in travel direction';

-- +goose Down
ALTER TABLE routing_edge
    DROP COLUMN IF EXISTS bearing_start,
    DROP COLUMN IF EXISTS bearing_end;
