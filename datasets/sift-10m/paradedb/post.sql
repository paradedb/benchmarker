CREATE INDEX sift_vector_idx ON sift
USING paradedb (_id, emb vector_l2_ops)
WITH (
    target_segment_count = 1,
    vector_router = 'ivf'
);

ALTER DATABASE benchmark SET paradedb.vector_cluster_max_probe = 0.01;
ALTER DATABASE benchmark SET paradedb.vector_recall_target = 0.95;

VACUUM ANALYZE sift;
