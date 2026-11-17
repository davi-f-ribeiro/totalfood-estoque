ALTER TABLE material_requests
  DROP CONSTRAINT material_requests_service_check,
  ADD CONSTRAINT material_requests_service_check
    CHECK (service IN ('cafe','almoco','lanche','jantar'));
