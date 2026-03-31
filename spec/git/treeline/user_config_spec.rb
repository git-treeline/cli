# frozen_string_literal: true

require "tmpdir"

RSpec.describe Git::Treeline::UserConfig do
  let(:tmpdir) { Dir.mktmpdir }
  let(:config_path) { File.join(tmpdir, "config.json") }

  after { FileUtils.rm_rf(tmpdir) }

  describe "defaults" do
    it "provides defaults when no config file exists" do
      config = described_class.new(config_path)

      expect(config.port_base).to eq(3000)
      expect(config.port_increment).to eq(10)
      expect(config.redis_strategy).to eq("prefixed")
      expect(config.redis_url).to eq("redis://localhost:6379")
    end
  end

  describe "with config file" do
    it "reads custom port settings" do
      File.write(config_path, JSON.generate(
                                "port" => { "base" => 4000, "increment" => 5 },
                                "redis" => { "strategy" => "prefixed", "url" => "redis://localhost:6379" }
                              ))
      config = described_class.new(config_path)

      expect(config.port_base).to eq(4000)
      expect(config.port_increment).to eq(5)
    end

    it "merges partial config with defaults" do
      File.write(config_path, JSON.generate("port" => { "base" => 5000 }))
      config = described_class.new(config_path)

      expect(config.port_base).to eq(5000)
      expect(config.port_increment).to eq(10)
      expect(config.redis_strategy).to eq("prefixed")
    end

    it "recovers from corrupt JSON" do
      File.write(config_path, "not valid json {{{")
      config = described_class.new(config_path)

      expect(config.port_base).to eq(3000)
    end
  end

  describe "#init!" do
    it "writes default config to disk" do
      config = described_class.new(config_path)
      config.init!

      expect(File.exist?(config_path)).to be true
      data = JSON.parse(File.read(config_path))
      expect(data["port"]["base"]).to eq(3000)
    end

    it "creates parent directories" do
      nested_path = File.join(tmpdir, "nested", "dir", "config.json")
      config = described_class.new(nested_path)
      config.init!

      expect(File.exist?(nested_path)).to be true
    end
  end

  describe "#config_file_exists?" do
    it "returns false when no file" do
      config = described_class.new(config_path)
      expect(config.config_file_exists?).to be false
    end

    it "returns true when file exists" do
      File.write(config_path, "{}")
      config = described_class.new(config_path)
      expect(config.config_file_exists?).to be true
    end
  end
end
