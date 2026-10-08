INSERT INTO activity_types (user_id, code, label, tracks_quantity, quantity_unit, tracks_duration, position)
VALUES (NULL, 'LB', 'Lecture biblique', TRUE,  'chapitres', FALSE, 1),
       (NULL, 'PS', 'Prière seule',     FALSE, NULL,        TRUE,  2),
       (NULL, 'RDQAD', 'Méditation',     TRUE, 'Nombre de fois',TRUE,  3),
       (NULL, 'PG', 'Prière en groupe',     TRUE, 'Nombre de fois',        TRUE,  4),
       (NULL, 'LLC', 'Lecture de la littérature chrétienne',     TRUE, 'Nombre de pages',        TRUE,  5),
       (NULL, 'JC', 'Jeûne complet',     TRUE, 'Nombre de fois',        FALSE,  6),
       (NULL, 'JP', 'Jeûne partiel',     TRUE, 'Nombre de fois',        FALSE,  7),
       (NULL, 'Dîmes', 'Dîmes',     TRUE, 'Pourcentage',        FALSE,  8),
       (NULL, 'OFF', 'Offrandes',     TRUE, 'Pourcentage',        FALSE,  9)
    ON CONFLICT ON CONSTRAINT uq_activity_types_owner_code DO NOTHING;