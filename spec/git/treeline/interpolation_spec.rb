# frozen_string_literal: true

RSpec.describe Git::Treeline::Interpolation do
  let(:allocation) do
    {
      port: 3010,
      database: "myapp_dev_feature",
      redis_prefix: "myapp:feature",
      redis_db: nil,
      worktree_name: "feature"
    }
  end

  describe ".build_redis_url" do
    it "returns base URL when no redis_db" do
      result = described_class.build_redis_url("redis://localhost:6379", allocation)
      expect(result).to eq("redis://localhost:6379")
    end

    it "appends db number when redis_db present" do
      alloc = allocation.merge(redis_db: 3)
      result = described_class.build_redis_url("redis://localhost:6379", alloc)
      expect(result).to eq("redis://localhost:6379/3")
    end

    it "strips trailing slash from base URL" do
      result = described_class.build_redis_url("redis://localhost:6379/", allocation)
      expect(result).to eq("redis://localhost:6379")
    end

    it "works with string keys (from JSON)" do
      alloc = { "redis_db" => 5, "redis_prefix" => nil }
      result = described_class.build_redis_url("redis://localhost:6379", alloc)
      expect(result).to eq("redis://localhost:6379/5")
    end
  end

  describe ".interpolate" do
    it "replaces all placeholders" do
      result = described_class.interpolate(
        "http://localhost:{port}",
        allocation: allocation, redis_url: "redis://localhost:6379", project: "myapp"
      )
      expect(result).to eq("http://localhost:3010")
    end

    it "replaces database placeholder" do
      result = described_class.interpolate(
        "{database}",
        allocation: allocation, redis_url: "redis://localhost:6379", project: "myapp"
      )
      expect(result).to eq("myapp_dev_feature")
    end

    it "replaces multiple placeholders in one pattern" do
      result = described_class.interpolate(
        "{project} on :{port} db:{database}",
        allocation: allocation, redis_url: "redis://localhost:6379", project: "myapp"
      )
      expect(result).to eq("myapp on :3010 db:myapp_dev_feature")
    end

    it "works with string keys" do
      alloc = {
        "port" => 3020,
        "database" => "test_db",
        "redis_prefix" => "test:wt",
        "worktree_name" => "wt"
      }
      result = described_class.interpolate(
        "{port}/{database}",
        allocation: alloc, redis_url: "redis://localhost:6379", project: "test"
      )
      expect(result).to eq("3020/test_db")
    end

    it "replaces {port_N} tokens from ports array" do
      alloc = allocation.merge(ports: [3010, 3011, 3012])
      result = described_class.interpolate(
        "{port}:{port_2}:{port_3}",
        allocation: alloc, redis_url: "redis://localhost:6379", project: "myapp"
      )
      expect(result).to eq("3010:3011:3012")
    end

    it "replaces {port_N} with string keys" do
      alloc = {
        "port" => 3020,
        "ports" => [3020, 3021],
        "database" => "test_db",
        "redis_prefix" => "test:wt",
        "worktree_name" => "wt"
      }
      result = described_class.interpolate(
        "{port}/{port_2}",
        allocation: alloc, redis_url: "redis://localhost:6379", project: "test"
      )
      expect(result).to eq("3020/3021")
    end

    it "leaves {port_N} untouched when no ports array" do
      result = described_class.interpolate(
        "{port}/{port_2}",
        allocation: allocation, redis_url: "redis://localhost:6379", project: "myapp"
      )
      expect(result).to eq("3010/{port_2}")
    end
  end
end
